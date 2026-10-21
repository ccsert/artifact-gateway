//go:build integration

package nuget

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
	"github.com/google/uuid"
)

func durableSourceModule(t *testing.T, store *repository.PostgresStore, objects objectstore.Store, feed *testFeed) *Module {
	t.Helper()
	auth := authorization.Authenticator{AdminToken: "nuget-test-admin", AdminActor: "operator", APIKeys: store, ServiceAccounts: store}
	m, err := New(Options{Store: store, Repositories: store, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: store, Capacities: store, Sources: store, Configuration: store, Groups: store, Upstream: testSourceClient{feed.server.Client()}})
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func TestPostgresRustFSNuGetProxyGroupRestartAndLostResponse(t *testing.T) {
	f := newDurableFixture(t)
	body := packageFixture(t, "Acme.Proxy", "1.0.0", "")
	feed := newTestFeed(t, body, packageFixture(t, "Acme.Proxy", "2.0.0", ""))
	proxy, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-proxy-" + uuid.NewString(), Format: repository.FormatNuGet, Type: repository.RepositoryTypeProxy, Endpoint: feed.server.URL + "/not-nuget-org/discover.json"})
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := f.store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "nuget-group-" + uuid.NewString(), Format: repository.FormatNuGet, Members: []repository.GroupMember{{RepositoryID: proxy.ID, Position: 0}, {RepositoryID: f.repo.ID, Position: 1}}}, "operator", uuid.NewString(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	f.module = durableSourceModule(t, f.store, f.objects, feed)
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		f.module.ServeHTTP(recorder, r)
		lost := false
		once.Do(func() { lost = true })
		if lost && recorder.Code == 200 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		for name, values := range recorder.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	t.Cleanup(server.Close)
	path := "/nuget/" + group.Name + "/v3/flat/acme.proxy/1.0.0/acme.proxy.1.0.0.nupkg"
	send := func() (int, []byte, error) {
		r, _ := http.NewRequest("GET", server.URL+path, nil)
		r.Header.Set("Authorization", "Bearer nuget-test-admin")
		response, err := server.Client().Do(r)
		if err != nil {
			return 0, nil, err
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		return response.StatusCode, body, err
	}
	_, _, _ = send() // The first committed response is deliberately lost.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, got, err := send()
			if err != nil || status != 200 || !bytes.Equal(got, body) {
				t.Errorf("concurrent native retry: status=%d err=%v", status, err)
			}
		}()
	}
	wg.Wait()
	publication, err := f.store.GetNuGetPublication(t.Context(), proxy.ID, "acme.proxy", "1.0.0")
	if err != nil || publication.Digest != digestFor(body) || publication.Size != int64(len(body)) {
		t.Fatalf("durable proxy identity: %+v %v", publication, err)
	}
	feed.mu.Lock()
	downloads := feed.requests["/immutable-files/acme.proxy/1.0.0/acme.proxy.1.0.0.nupkg"]
	feed.mu.Unlock()
	if downloads != 1 {
		t.Fatalf("concurrent package downloads=%d", downloads)
	}
	capacity, err := f.store.GetRepositoryCapacity(t.Context(), proxy.ID)
	if err != nil || capacity.ObjectCount != 1 || capacity.UsedBytes != int64(len(body)) {
		t.Fatalf("cache quota: %+v %v", capacity, err)
	}
	group, err = f.store.ReplaceHostedGroupMembers(t.Context(), group.ID, []repository.GroupMember{{RepositoryID: f.repo.ID, Position: 0}, {RepositoryID: proxy.ID, Position: 1}}, group.Version)
	if err != nil {
		t.Fatal(err)
	}
	server.Close()
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	m := durableSourceModule(t, reopened, f.objects, feed)
	doc, err := reopened.GetNuGetSourceDocument(t.Context(), proxy.ID, proxy.Version, proxy.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	doc.FetchedAt = time.Now().UTC().Add(-time.Minute)
	doc.ExpiresAt = doc.FetchedAt.Add(time.Second)
	if err = reopened.PutNuGetSourceDocument(t.Context(), doc); err != nil {
		t.Fatal(err)
	}
	feed.server.Close()
	w := request(t, m, "GET", path, nil, bearer)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("reopened offline Group: %d %s", w.Code, w.Body.String())
	}
	owner, err := reopened.GetNuGetGroupAuthority(t.Context(), group.ID, "acme.proxy", "1.0.0")
	if err != nil || owner.RepositoryID != proxy.ID || owner.Digest != publication.Digest || owner.Size != publication.Size {
		t.Fatalf("durable authority: %+v %v", owner, err)
	}
	if err = f.objects.Delete(t.Context(), publication.ObjectKey); err != nil {
		t.Fatal(err)
	}
	w = request(t, m, "GET", path, nil, bearer)
	if w.Code == 200 {
		t.Fatal("incomplete cache succeeded after reopen")
	}
}
func TestPostgresRustFSNuGetProxySourceCredentialBindingAndQuota(t *testing.T) {
	t.Setenv(secrets.KeyEnv, strings.Repeat("52", 32))
	f := newDurableFixture(t)
	body := packageFixture(t, "Acme.Credential", "1.0.0", "")
	feed := newTestFeed(t, body)
	sealed, err := secrets.Seal(upstreamauth.SecretPurpose, "source-password")
	if err != nil {
		t.Fatal(err)
	}
	feed.authorization = "Bearer source-password"
	proxy, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-secret-" + uuid.NewString(), Format: repository.FormatNuGet, Type: repository.RepositoryTypeProxy, Endpoint: feed.server.URL + "/not-nuget-org/discover.json", UpstreamAuth: &repository.UpstreamAuth{Scheme: repository.UpstreamAuthSchemeBearer, Secret: sealed}})
	if err != nil {
		t.Fatal(err)
	}
	m := durableSourceModule(t, f.store, f.objects, feed)
	path := "/nuget/" + proxy.Name + "/v3/flat/acme.credential/1.0.0/acme.credential.1.0.0.nupkg"
	if _, err = f.store.ReplaceRepositoryCapacityQuota(t.Context(), proxy.ID, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	w := request(t, m, "GET", path, nil, bearer)
	if w.Code != 200 {
		t.Fatalf("private proxy cold: %d %s", w.Code, w.Body.String())
	}
	// New source credentials create a new immutable cache namespace. Its
	// download cannot evade the existing repository's retained byte quota.
	proxy.UpstreamAuth.Secret, err = secrets.Seal(upstreamauth.SecretPurpose, "rotated-password")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err = f.store.UpdateHostedRepository(t.Context(), proxy, proxy.Version)
	if err != nil {
		t.Fatal(err)
	}
	feed.mu.Lock()
	feed.authorization = "Bearer rotated-password"
	feed.mu.Unlock()
	w = request(t, m, "GET", path, nil, bearer)
	if w.Code != 409 {
		t.Fatalf("rotation bypassed byte quota: %d %s", w.Code, w.Body.String())
	}
	if _, err = f.store.ReplaceRepositoryCapacityQuota(t.Context(), proxy.ID, int64(2*len(body))); err != nil {
		t.Fatal(err)
	}
	w = request(t, m, "GET", path, nil, bearer)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatalf("rotated private source: %d %s", w.Code, w.Body.String())
	}
	proxy.UpstreamAuth.Secret = "corrupt-source-ciphertext"
	if proxy, err = f.store.UpdateHostedRepository(t.Context(), proxy, proxy.Version); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		w = request(t, m, "GET", path, nil, bearer)
		if w.Code != 502 {
			t.Fatalf("bad source credential used warm cache: %d %s", w.Code, w.Body.String())
		}
	}
	audits, err := f.store.ListAudits(t.Context(), repository.AuditQuery{Repository: proxy.Name, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(audits)
	for _, secret := range []string{"source-password", "rotated-password", sealed, "corrupt-source-ciphertext"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("source credential leaked to audit")
		}
	}
}

func TestPostgresRustFSNuGetProxyGroupLocalCredentialRevocation(t *testing.T) {
	f := newDurableFixture(t)
	body := packageFixture(t, "Acme.Identity", "1.0.0", "")
	feed := newTestFeed(t, body)
	proxy, err := f.store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "nuget-identity-" + uuid.NewString(), Format: repository.FormatNuGet, Type: repository.RepositoryTypeProxy, Endpoint: feed.server.URL + "/not-nuget-org/discover.json", AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	f.repo.AnonymousRead = true
	f.repo, err = f.store.UpdateHostedRepository(t.Context(), f.repo, f.repo.Version)
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := f.store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "nuget-auth-group-" + uuid.NewString(), Format: repository.FormatNuGet, AnonymousRead: true, Members: []repository.GroupMember{{RepositoryID: proxy.ID, Position: 0}, {RepositoryID: f.repo.ID, Position: 1}}}, "operator", uuid.NewString(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	store := f.store
	account, err := store.CreateServiceAccount(t.Context(), repository.ServiceAccount{ID: uuid.NewString(), Name: "nuget-group-auth-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	actor := "service-account:" + account.ID
	grants := []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:read"}, ResourcePrefix: "acme."}}
	for _, repo := range []repository.HostedRepository{proxy, f.repo} {
		if _, err = store.ReplaceRepositoryGrants(t.Context(), repo.ID, grants, "1"); err != nil {
			t.Fatal(err)
		}
	}
	blue, green := "agc_nuget-proxy-blue-"+uuid.NewString(), "agc_nuget-proxy-green-"+uuid.NewString()
	credential := func(name, token string) repository.APIKey {
		t.Helper()
		expires := time.Now().UTC().Add(time.Hour)
		key, err := store.CreateServiceAccountCredential(t.Context(), repository.APIKey{ID: uuid.NewString(), ServiceAccountID: account.ID, Name: name, SecretHash: authorization.HashAPIKey(token), Roles: []string{}, ExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	blueKey := credential("blue", blue)
	credential("green", green)
	// Only the global anonymous toggle is an isolated test port. Identity,
	// credentials, grants, member configuration and authority all use real PG.
	policy := repository.NewMemoryStore()
	if _, err = policy.ReplaceAnonymousAccessPolicy(t.Context(), repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	newServer := func() *httptest.Server {
		t.Helper()
		auth := authorization.Authenticator{APIKeys: store, ServiceAccounts: store}
		m, err := New(Options{Store: store, Repositories: store, Objects: f.objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: policy, Sources: store, Configuration: store, Groups: store, Upstream: testSourceClient{feed.server.Client()}})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(m)
		t.Cleanup(server.Close)
		return server
	}
	server := newServer()
	paths := []string{"/nuget/" + proxy.Name + "/v3/flat/acme.identity/1.0.0/acme.identity.1.0.0.nupkg", "/nuget/" + group.Name + "/v3/flat/acme.identity/1.0.0/acme.identity.1.0.0.nupkg"}
	assert := func(token string, status int) {
		t.Helper()
		for _, path := range paths {
			r, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+path, nil)
			if token != "" {
				r.SetBasicAuth("not-a-principal", token)
			}
			response, err := server.Client().Do(r)
			if err != nil {
				t.Fatal("native private NuGet request failed")
			}
			got, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != status || status == 200 && !bytes.Equal(got, body) {
				t.Fatalf("native credential status=%d want=%d", response.StatusCode, status)
			}
			if bytes.Contains(got, []byte(blue)) || bytes.Contains(got, []byte(green)) {
				t.Fatal("local credential leaked")
			}
		}
	}
	assert(blue, 200)
	assert(green, 200)
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	server = newServer()
	assert(blue, 200)
	assert(green, 200)
	if _, err = store.RevokeServiceAccountCredential(t.Context(), account.ID, blueKey.ID); err != nil {
		t.Fatal(err)
	}
	assert("", 200)
	assert(blue, 401)
	assert(green, 200)
	set, err := store.GetRepositoryGrants(t.Context(), proxy.ID)
	if err != nil {
		t.Fatal(err)
	}
	set, err = store.ReplaceRepositoryGrants(t.Context(), proxy.ID, []repository.RepositoryGrant{}, set.Version)
	if err != nil {
		t.Fatal(err)
	}
	assert(green, 403)
	if _, err = store.ReplaceRepositoryGrants(t.Context(), proxy.ID, grants, set.Version); err != nil {
		t.Fatal(err)
	}
	assert(green, 200)
	for _, name := range []string{proxy.Name, group.Name} {
		r, _ := http.NewRequest("GET", server.URL+"/nuget/"+name+"/v3/flat/other.identity/index.json", nil)
		r.SetBasicAuth("ignored", green)
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal("prefix denial fell through")
		}
	}
	disabled := repository.ServiceAccountDisabled
	if _, err = store.UpdateServiceAccount(t.Context(), repository.ServiceAccountUpdate{ID: account.ID, State: &disabled}, account.Version); err != nil {
		t.Fatal(err)
	}
	assert("", 200)
	assert(green, 401)
	audits, err := store.ListAudits(t.Context(), repository.AuditQuery{Repository: group.Name, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(audits)
	if bytes.Contains(encoded, []byte(blue)) || bytes.Contains(encoded, []byte(green)) {
		t.Fatal("credential leaked to durable audit")
	}
	resolved := 0
	for _, audit := range audits {
		if audit.Operation == "nuget.group.resolve" && audit.Actor != "anonymous" {
			resolved++
			if audit.Actor != actor {
				t.Fatal("rotation changed the stable audit principal")
			}
		}
	}
	if resolved < 4 {
		t.Fatal("persistent Group resolution audit missing")
	}
}
