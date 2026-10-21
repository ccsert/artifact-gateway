package app

import (
	"bytes"
	"encoding/base64"
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
