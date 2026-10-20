package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPrivateRawProxyCredentials(t *testing.T) {
	for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
		for _, groupRead := range []bool{false, true} {
			t.Run(string(scheme)+map[bool]string{false: "/proxy", true: "/group"}[groupRead], func(t *testing.T) {
				privateRawCredentialsContract(t, scheme, groupRead, privateProxyBackend{store: repository.NewMemoryStore(), objects: objectstore.NewMemoryStore(), cache: objectstore.NewMemoryStore()})
			})
		}
	}
}

func privateRawCredentialsContract(t *testing.T, scheme repository.UpstreamAuthScheme, groupRead bool, backend privateProxyBackend) {
	t.Helper()
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	ctx := t.Context()
	store, objects := backend.store, backend.objects
	var calls, fallbackCalls, refusalStatus atomic.Int32
	refusalStatus.Store(http.StatusUnauthorized)
	var expected atomic.Value
	expected.Store(privateProxyAuthorization(scheme, "raw-source-first-secret"))
	body := []byte("complete private Raw bytes\x00\xff")
	upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host == redirectCDN {
			fallbackCalls.Add(1)
			_, _ = w.Write(body)
			return
		}
		calls.Add(1)
		if r.Header.Get("Authorization") != expected.Load().(string) {
			http.Error(w, "raw-source-first-secret", int(refusalStatus.Load()))
			return
		}
		_, _ = w.Write(body)
	})
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "private-raw-" + uuid.NewString(), Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin}, UpstreamAuth: &repository.UpstreamAuth{Scheme: scheme, Username: map[bool]string{true: "source-reader"}[scheme == repository.UpstreamAuthSchemeBasic], Secret: sealedUpstreamSecret(t, "raw-source-first-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	name := repo.Name
	if groupRead {
		fallback, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "raw-fallback-" + uuid.NewString(), Format: repository.FormatRaw, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectCDN, AllowedHosts: []string{redirectCDN}})
		if err != nil {
			t.Fatal(err)
		}
		configured, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "raw-auth-group-" + uuid.NewString(), Format: repository.FormatRaw, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}, {RepositoryID: fallback.ID, Position: 1}}}, "private-raw", uuid.NewString(), "fixture")
		if err != nil {
			t.Fatal(err)
		}
		name = configured.Name
	}
	makeHandler := func() http.Handler {
		return newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: SharedObjectStores(objects), Upstream: upstream.client, Caches: GatewayCaches{Raw: NewDefaultRawCache(backend.cache, []string{redirectOrigin})}})
	}
	handler := makeHandler()
	get := func(want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/raw/"+name+"/private.bin", nil)
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want || want == 200 && !bytes.Equal(w.Body.Bytes(), body) {
			t.Fatalf("Raw status=%d body=%q", w.Code, w.Body.Bytes())
		}
		if want != 200 && strings.Contains(w.Body.String(), "raw-source-first-secret") {
			t.Fatal("upstream error secret leaked")
		}
	}
	assets := map[string]redirectCacheAsset{"/private.bin": {body: body}}
	if backend.client != nil {
		backend.client(t, handler, name, "cold", assets)
	}
	get(200)
	before := calls.Load()
	get(200)
	if calls.Load() != before {
		t.Fatal("verified cache miss")
	}
	if backend.reload != nil {
		store, backend.cache = backend.reload()
		handler = makeHandler()
		get(200)
		if calls.Load() != before {
			t.Fatal("Raw persistent cache missed after store reload")
		}
	}
	if backend.client != nil {
		backend.client(t, handler, name, "hot", assets)
	}
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "")
	get(502)
	if calls.Load() != before {
		t.Fatal("Raw missing key contacted source")
	}
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	privateRejectSourceRebinding(t, handler, store, repo, func() int32 { return calls.Load() + fallbackCalls.Load() })
	beforeCorruption := calls.Load()
	repo = privateCorruptSourceCredential(t, store, repo, func() { get(502) })
	if calls.Load() != beforeCorruption {
		t.Fatal("Raw damaged credential contacted source")
	}
	patch := func(auth map[string]string) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"upstreamAuth": auth})
		r := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, bytes.NewReader(data))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", repo.Version)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("patch=%d %s", w.Code, w.Body.String())
		}
		repo, err = store.GetHostedRepository(ctx, repo.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	expected.Store(privateProxyAuthorization(scheme, "raw-source-rotated-secret"))
	patch(map[string]string{"scheme": string(scheme), "username": repo.UpstreamAuth.Username, "secret": "wrong-rotated-secret"})
	for i := 0; i < 4; i++ {
		get(502)
	}
	patch(map[string]string{"scheme": string(scheme), "username": repo.UpstreamAuth.Username, "secret": "raw-source-rotated-secret"})
	get(200)
	get(200)
	refusalStatus.Store(http.StatusForbidden)
	patch(map[string]string{"scheme": "none"})
	for i := 0; i < 4; i++ {
		get(502)
	}
	if fallbackCalls.Load() != 0 {
		t.Fatal("Raw credential failure contacted anonymous fallback")
	}
	privateAssertRedactedManagementAndAudit(t, handler, store, repo, "raw-source-first-secret", "raw-source-rotated-secret", "wrong-rotated-secret", "damaged-source-ciphertext")
}
