//go:build integration

package nuget

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSNuGetCredentialRotationAndRevocation(t *testing.T) {
	f := newDurableFixture(t)
	ctx := t.Context()
	store := f.store
	account, err := store.CreateServiceAccount(ctx, repository.ServiceAccount{ID: uuid.NewString(), Name: "nuget-auth-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	actor := "service-account:" + account.ID
	grants := []repository.RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:write"}, ResourcePrefix: "acme."}}
	if _, err = store.ReplaceRepositoryGrants(ctx, f.repo.ID, grants, "1"); err != nil {
		t.Fatal(err)
	}
	blue, green := "agc_nuget-blue-"+uuid.NewString(), "agc_nuget-green-"+uuid.NewString()
	createCredential := func(name, token string) repository.APIKey {
		t.Helper()
		expires := time.Now().UTC().Add(time.Hour)
		key, err := store.CreateServiceAccountCredential(ctx, repository.APIKey{ID: uuid.NewString(), ServiceAccountID: account.ID, Name: name, SecretHash: authorization.HashAPIKey(token), ExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	blueKey := createCredential("blue", blue)
	// Keep global PostgreSQL settings untouched in the shared integration DB.
	// This explicit policy port enables anonymous reads for this test's own repo.
	anonymous := repository.NewMemoryStore()
	if _, err = anonymous.ReplaceAnonymousAccessPolicy(ctx, repository.AnonymousAccessPolicy{Enabled: true}, "1"); err != nil {
		t.Fatal(err)
	}
	f.repo.AnonymousRead = true
	if f.repo, err = store.UpdateHostedRepository(ctx, f.repo, f.repo.Version); err != nil {
		t.Fatal(err)
	}
	serve := func() *httptest.Server {
		t.Helper()
		auth := authorization.Authenticator{APIKeys: store, ServiceAccounts: store}
		module, err := New(Options{Store: store, Repositories: store, Objects: f.objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: store, Legacy: auth}, Audit: store, Anonymous: anonymous})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(module)
		t.Cleanup(server.Close)
		return server
	}
	server := serve()
	body := packageFixture(t, "Acme.Auth", "1.0.0", "")
	path := "/nuget/" + f.repo.Name
	readPath := path + "/v3/flat/acme.auth/1.0.0/acme.auth.1.0.0.nupkg"
	send := func(method, url, token string, data []byte, want int) []byte {
		t.Helper()
		contentType := ""
		if method == http.MethodPut {
			data, contentType = multipartPackage(t, data)
		}
		req, err := http.NewRequestWithContext(ctx, method, server.URL+url, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPut {
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-NuGet-ApiKey", token)
		} else if token != "" {
			req.SetBasicAuth("not-an-identity", token)
		}
		client := server.Client()
		client.Timeout = 10 * time.Second
		response, err := client.Do(req)
		if err != nil {
			t.Fatal("native NuGet HTTP request failed")
		}
		defer func() { _ = response.Body.Close() }()
		data, err = io.ReadAll(response.Body)
		if err != nil || bytes.Contains(data, []byte(blue)) || bytes.Contains(data, []byte(green)) {
			t.Fatal("native NuGet response unreadable or exposes a credential")
		}
		if response.StatusCode != want {
			t.Fatalf("native %s status=%d want=%d", method, response.StatusCode, want)
		}
		return data
	}
	assertAccess := func(label, token string, read, push int) {
		t.Helper()
		if data := send(http.MethodGet, readPath, token, nil, read); read == 200 && !bytes.Equal(data, body) {
			t.Fatal("native package bytes changed")
		}
		send(http.MethodPut, path+"/api/v2/package/", token, body, push)
		t.Logf("%s: native GET=%d push=%d", label, read, push)
	}
	send(http.MethodPut, path+"/api/v2/package/", blue, body, 201)
	assertAccess("blue active", blue, 200, 201)
	greenKey := createCredential("green", green)
	assertAccess("blue during rotation", blue, 200, 201)
	assertAccess("green during rotation", green, 200, 201)
	server.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server = serve()
	assertAccess("blue after PostgreSQL store reopen", blue, 200, 201)
	assertAccess("green after PostgreSQL store reopen", green, 200, 201)
	if _, err = store.RevokeServiceAccountCredential(ctx, account.ID, blueKey.ID); err != nil {
		t.Fatal(err)
	}
	assertAccess("revoked blue", blue, 401, 401)
	if data := send(http.MethodGet, readPath, "", nil, 200); !bytes.Equal(data, body) {
		t.Fatal("anonymous control after real revocation changed package bytes")
	}
	t.Log("anonymous GET=200 while the persisted revoked credential GET/push=401")
	assertAccess("green after blue revocation", green, 200, 201)
	other := packageFixture(t, "Other.Auth", "1.0.0", "")
	send(http.MethodGet, path+"/v3/flat/other.auth/index.json", green, nil, 403)
	send(http.MethodPut, path+"/api/v2/package/", green, other, 403)
	set, err := store.GetRepositoryGrants(ctx, f.repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	set, err = store.ReplaceRepositoryGrants(ctx, f.repo.ID, nil, set.Version)
	if err != nil {
		t.Fatal(err)
	}
	assertAccess("grant removed", green, 403, 403)
	if _, err = store.ReplaceRepositoryGrants(ctx, f.repo.ID, grants, set.Version); err != nil {
		t.Fatal(err)
	}
	assertAccess("same principal grant restored", green, 200, 201)
	disabled := repository.ServiceAccountDisabled
	if _, err = store.UpdateServiceAccount(ctx, repository.ServiceAccountUpdate{ID: account.ID, State: &disabled}, account.Version); err != nil {
		t.Fatal(err)
	}
	assertAccess("account disabled", green, 401, 401)
	if data := send(http.MethodGet, readPath, "", nil, 200); !bytes.Equal(data, body) {
		t.Fatal("anonymous control did not read the same package")
	}
	keys, err := store.ListServiceAccountCredentials(ctx, account.ID, 10, "")
	if err != nil || len(keys) != 2 {
		t.Fatal("persistent rotated credentials missing")
	}
	for _, key := range keys {
		if key.ServiceAccountID != account.ID || key.LastUsedAt == nil || key.ID == blueKey.ID && key.RevokedAt == nil || key.ID == greenKey.ID && key.RevokedAt != nil {
			t.Fatal("persistent credential identity, revocation or use changed")
		}
	}
	publications, err := store.ListNuGetPublications(ctx, f.repo.ID, "acme.auth", 10, "")
	if err != nil || len(publications) != 1 {
		t.Fatal("credential rotation duplicated visible publication")
	}
	audits, err := store.ListAudits(ctx, repository.AuditQuery{Repository: f.repo.Name, Limit: 100})
	if err != nil || len(audits) == 0 {
		t.Fatal("native authorization audit missing")
	}
	encoded, err := json.Marshal(audits)
	if err != nil || bytes.Contains(encoded, []byte(blue)) || bytes.Contains(encoded, []byte(green)) {
		t.Fatal("native audit exposes a credential")
	}
	publicationAudits, deniedAudits := 0, 0
	for _, audit := range audits {
		if audit.Operation == "nuget.publish" {
			publicationAudits++
		} else if audit.Outcome == repository.AuditAccessDenied {
			deniedAudits++
		} else {
			continue
		}
		if audit.Actor != actor {
			t.Fatal("rotated credential changed the stable audit principal")
		}
	}
	if publicationAudits != 1 || deniedAudits < 4 {
		t.Fatal("native publication or scoped denial audit missing")
	}
}
