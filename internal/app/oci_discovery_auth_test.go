package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestOCIReferrersExplicitCredentialsNeverBecomeAnonymous(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	objects := objectstore.NewMemoryStore()
	testOCIExplicitCredentials(t, store, store, objects, objects)
}

func TestOCIReferrersAnonymousIdentityNeverSharesAuthenticatedAuthority(t *testing.T) {
	for _, adminFirst := range []bool{false, true} {
		t.Run(fmt.Sprint("adminFirst=", adminFirst), func(t *testing.T) {
			store := repository.NewMemoryStore()
			enableAnonymousAccess(t, store)
			objects := objectstore.NewMemoryStore()
			testOCIAnonymousIdentityPartition(t, store, store, objects, objects, adminFirst)
		})
	}
}

func testOCIAnonymousIdentityPartition(t *testing.T, store GatewayStore, persistence discovery.Store, objects, control ObjectStore, adminFirst bool) {
	t.Helper()
	a, b := newDiscoveryArtifactFixture(t, "private authority A"), newDiscoveryArtifactFixture(t, "anonymous authority B")
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/a/") {
			a.serve(w, r)
		} else {
			b.serve(w, r)
		}
	})
	ra, rb := catalogProxy(t, store, "identity-a-"+uuid.NewString()[:8]), catalogProxy(t, store, "identity-b-"+uuid.NewString()[:8])
	var err error
	ra.Endpoint += "/a"
	ra, err = store.UpdateHostedRepository(t.Context(), ra, ra.Version)
	if err != nil {
		t.Fatal(err)
	}
	rb.Endpoint += "/b"
	rb.AnonymousRead = true
	rb, err = store.UpdateHostedRepository(t.Context(), rb, rb.Version)
	if err != nil {
		t.Fatal(err)
	}
	catalogReader(t, store, ra, anonymousActor, "team/")
	group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "identity-group-" + uuid.NewString()[:8], Format: repository.FormatOCI, AnonymousRead: true, Members: []repository.GroupMember{{RepositoryID: ra.ID}, {RepositoryID: rb.ID, Position: 1}}}, "fixture-admin", uuid.NewString(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticator()
	auth.AdminActor = anonymousActor
	principal, authenticated := auth.Authenticate("Bearer " + auth.AdminToken)
	if !authenticated || !principal.Admin || principal.AuthenticationKind == "" {
		t.Fatal("the configured same-name administrator did not really authenticate")
	}
	gateway := func(auth Authenticator) http.Handler {
		return newTestGateway(t, GatewayOptions{Store: store, OCIDiscoveryStore: persistence, Adapter: TestAdapter{}, Authenticator: auth, Storage: ObjectStores{repository.FormatOCI: objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(control, []string{redirectOrigin, redirectCDN})}, Upstream: fixture.client})
	}
	handler := gateway(auth)
	path := "/v2/" + group.Name + "/team/app/referrers/" + a.subject.Digest
	check := func(h http.Handler, token string, expected, absent discoveryArtifactFixture) {
		t.Helper()
		response := discoveryRequest(t, h, path, token)
		if response.Code != 200 || !strings.Contains(response.Body.String(), expected.refs[0].Digest) || strings.Contains(response.Body.String(), absent.refs[0].Digest) {
			t.Fatalf("anonymous and authenticated authority collided: authenticated=%t status=%d body=%s", token != "", response.Code, response.Body.String())
		}
		for _, digest := range []string{expected.subject.Digest, expected.refs[0].Digest} {
			original := discoveryRequest(t, h, "/v2/"+group.Name+"/team/app/manifests/"+digest, token)
			if original.Code != 200 || !bytes.Equal(original.Body.Bytes(), expected.objects[digest].body) {
				t.Fatalf("bound original bytes crossed authentication namespaces: status=%d body=%s", original.Code, original.Body.String())
			}
		}
	}
	if adminFirst {
		check(handler, auth.AdminToken, a, b)
		check(handler, "", b, a)
	} else {
		check(handler, "", b, a)
		check(handler, auth.AdminToken, a, b)
	}
	private := discoveryRequest(t, handler, "/v2/"+ra.Name+"/team/app/referrers/"+a.subject.Digest, auth.AdminToken)
	if private.Code != 200 || !strings.Contains(private.Body.String(), a.refs[0].Digest) {
		t.Fatalf("real same-name authenticated administrator was treated as anonymous: %d %s", private.Code, private.Body.String())
	}
	group.Members[0].Position, group.Members[1].Position = 1, 0
	if _, err := store.ReplaceHostedGroup(t.Context(), group, group.Version); err != nil {
		t.Fatal(err)
	}
	resolver := testAuthenticator()
	resolver.ResolverActor = anonymousActor
	resolver.AdminToken = "different-admin-credential"
	check(gateway(resolver), resolver.ResolverToken, a, b)
	check(handler, "", b, a)
	catalogReader(t, store, ra, "other-actor", "team/")
	denied := discoveryRequest(t, gateway(resolver), path, resolver.ResolverToken)
	if denied.Code != 403 {
		t.Fatalf("credential kind change or grant revocation replaced authority: %d %s", denied.Code, denied.Body.String())
	}
	check(handler, "", b, a)
}

func testOCIExplicitCredentials(t *testing.T, store GatewayStore, persistence discovery.Store, objects, control ObjectStore) {
	t.Helper()
	const payload = `{"SPDXID":"anonymous-auth-boundary"}`
	f := newDiscoveryArtifactFixture(t, payload)
	var calls atomic.Int32
	fixture := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("downstream credential reached the public upstream")
		}
		f.serve(w, r)
	})
	repo := catalogProxy(t, store, "auth-discovery-"+uuid.NewString()[:8])
	repo.AnonymousRead = true
	var err error
	repo, err = store.UpdateHostedRepository(t.Context(), repo, repo.Version)
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "auth-group-" + uuid.NewString()[:8], Format: repository.FormatOCI, AnonymousRead: true, Members: []repository.GroupMember{{RepositoryID: repo.ID}}}, "fixture-admin", uuid.NewString(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateServiceAccount(t.Context(), repository.ServiceAccount{ID: uuid.NewString(), Name: "auth-client-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	token := "agc_discovery_" + uuid.NewString()
	expires := time.Now().Add(time.Hour)
	credential, err := store.CreateServiceAccountCredential(t.Context(), repository.APIKey{ID: uuid.NewString(), ServiceAccountID: account.ID, Name: "revoked-client", SecretHash: authorization.HashAPIKey(token), Roles: []string{}, ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	catalogReader(t, store, repo, "service-account:"+account.ID, "team/")
	auth := testAuthenticator()
	auth.APIKeys, auth.ServiceAccounts = store, store
	handler := newTestGateway(t, GatewayOptions{Store: store, OCIDiscoveryStore: persistence, Adapter: TestAdapter{}, Authenticator: auth, Storage: ObjectStores{repository.FormatOCI: objects}, Caches: GatewayCaches{OCI: NewDefaultOCICache(control, []string{redirectOrigin, redirectCDN})}, Upstream: fixture.client})
	serve := func(method, path, header string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, name := range []string{repo.Name, group.Name} {
		path := "/v2/" + name + "/team/app/referrers/" + f.subject.Digest
		for _, header := range []string{"", "Bearer " + token} {
			positive := serve(http.MethodGet, path, header)
			if positive.Code != 200 || !strings.Contains(positive.Body.String(), f.refs[0].Digest) {
				t.Fatalf("anonymous/active credential positive control for %s=%d %s", name, positive.Code, positive.Body.String())
			}
		}
	}
	if _, err := store.RevokeServiceAccountCredential(t.Context(), account.ID, credential.ID); err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	for _, name := range []string{repo.Name, group.Name} {
		prefix := "/v2/" + name + "/team/app/"
		for _, resource := range []struct {
			path string
			body []byte
		}{
			{prefix + "referrers/" + f.subject.Digest, nil},
			{prefix + "manifests/" + f.refs[0].Digest, f.objects[f.refs[0].Digest].body},
			{prefix + "blobs/" + ocigraph.Digest([]byte(payload)), []byte(payload)},
		} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				for _, header := range []string{"Bearer explicit-invalid", "Basic " + base64.StdEncoding.EncodeToString([]byte("ignored:explicit-invalid")), "Bearer " + token, "Basic " + base64.StdEncoding.EncodeToString([]byte("ignored:"+token))} {
					rejected := serve(method, resource.path, header)
					if rejected.Code != 401 || rejected.Header().Get("WWW-Authenticate") == "" || calls.Load() != before {
						t.Fatalf("explicit failed credential fell back to anonymous: %s %s status=%d sourceCalls=%d/%d", method, resource.path, rejected.Code, calls.Load(), before)
					}
				}
				anonymous := serve(method, resource.path, "")
				if anonymous.Code != 200 || method == http.MethodHead && anonymous.Body.Len() != 0 || method == http.MethodGet && resource.body != nil && !bytes.Equal(anonymous.Body.Bytes(), resource.body) {
					t.Fatalf("genuine anonymous original-content control=%d %s", anonymous.Code, anonymous.Body.String())
				}
			}
		}
	}
	if calls.Load() != before {
		t.Fatal("authentication failure or cached anonymous control contacted upstream")
	}
}
