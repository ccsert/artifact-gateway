package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPrivateConanProxyCredentials(t *testing.T) {
	for _, scheme := range []repository.UpstreamAuthScheme{repository.UpstreamAuthSchemeBasic, repository.UpstreamAuthSchemeBearer} {
		for _, group := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/group=%v", scheme, group), func(t *testing.T) {
				privateConanCredentialsContract(t, scheme, group, privateProxyBackend{store: repository.NewMemoryStore(), objects: objectstore.NewMemoryStore(), cache: objectstore.NewMemoryStore()})
			})
		}
	}
}

func privateConanCredentialsContract(t *testing.T, scheme repository.UpstreamAuthScheme, group bool, backend privateProxyBackend) {
	t.Helper()
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	store := backend.store
	var expected atomic.Value
	expected.Store(privateProxyAuthorization(scheme, "conan-source-first-secret"))
	var tokens, requests, fallbackCalls, refusalStatus atomic.Int32
	refusalStatus.Store(http.StatusUnauthorized)
	fixture := newConanDownloadClient()
	var fixtureMu sync.Mutex
	issued := make(map[string]string)
	upstream := newProxyRedirectFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host == redirectCDN {
			fallbackCalls.Add(1)
		}
		if r.URL.Path == "/v2/users/authenticate" {
			if scheme != repository.UpstreamAuthSchemeBasic || r.Header.Get("Authorization") != expected.Load().(string) {
				http.Error(w, "conan-source-first-secret", int(refusalStatus.Load()))
				return
			}
			token := fmt.Sprintf("synthetic-conan-token-%d", tokens.Add(1))
			fixtureMu.Lock()
			issued["Bearer "+token] = expected.Load().(string)
			fixtureMu.Unlock()
			_, _ = io.WriteString(w, token)
			return
		}
		requests.Add(1)
		want := expected.Load().(string)
		fixtureMu.Lock()
		defer fixtureMu.Unlock()
		if scheme == repository.UpstreamAuthSchemeBasic {
			if issued[r.Header.Get("Authorization")] == want {
				want = r.Header.Get("Authorization")
			}
		}
		if r.Host != redirectCDN && (r.Header.Get("Authorization") != want || scheme == repository.UpstreamAuthSchemeBasic && tokens.Load() == 0) {
			http.Error(w, "conan-source-first-secret", int(refusalStatus.Load()))
			return
		}
		response, err := fixture.FetchConan(r.Context(), r.Method, repository.Member{}, strings.TrimPrefix(r.URL.Path, "/v2/conans/"), r.Header)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer func() { _ = response.Body.Close() }()
		for name, values := range response.Header {
			w.Header()[name] = values
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	})
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "private-conan-" + uuid.NewString(), Format: repository.FormatConan, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectOrigin, AllowedHosts: []string{redirectOrigin}, UpstreamAuth: &repository.UpstreamAuth{Scheme: scheme, Username: map[bool]string{true: "source-reader"}[scheme == repository.UpstreamAuthSchemeBasic], Secret: sealedUpstreamSecret(t, "conan-source-first-secret")}})
	if err != nil {
		t.Fatal(err)
	}
	name := repo.Name
	if group {
		fallback, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "public-conan-" + uuid.NewString(), Format: repository.FormatConan, Type: repository.RepositoryTypeProxy, Endpoint: "https://" + redirectCDN, AllowedHosts: []string{redirectCDN}})
		if err != nil {
			t.Fatal(err)
		}
		g, _, err := store.CreateHostedGroupIdempotently(t.Context(), repository.HostedGroup{ID: uuid.NewString(), Name: "private-conan-group-" + uuid.NewString(), Format: repository.FormatConan, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}, {RepositoryID: fallback.ID, Position: 1}}}, "auth-fixture", uuid.NewString(), "fixture")
		if err != nil {
			t.Fatal(err)
		}
		name = g.Name
	}
	makeHandler := func() http.Handler {
		return newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator(), Storage: SharedObjectStores(backend.objects), Upstream: upstream.client, Caches: GatewayCaches{Conan: NewDefaultConanCache(backend.cache, []string{redirectOrigin, redirectCDN})}})
	}
	handler := makeHandler()
	const root = "/v2/conans/pkg/1.0/user/stable"
	const recipe = root + "/revisions/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/files/conanfile.py"
	get := func(path string, want int, full bool) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/conan/"+name+path, nil)
		authorize(r, "admin-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("Conan %s=%d want=%d body=%s", path, w.Code, want, w.Body.String())
		}
		if full && !bytes.Equal(w.Body.Bytes(), fixture.files["conanfile.py"]) {
			t.Fatal("Conan full file bytes changed")
		}
		if strings.Contains(w.Body.String(), "conan-source-first-secret") {
			t.Fatal("Conan secret leaked")
		}
	}
	assets := map[string]redirectCacheAsset{"/conanfile.py": {body: fixture.files["conanfile.py"]}}
	if backend.client != nil {
		backend.client(t, handler, name, "cold", assets)
	}
	get(root+"/revisions", 200, false)
	get(recipe, 200, true)
	before := requests.Load()
	get(recipe, 200, true)
	if requests.Load() != before {
		t.Fatal("Conan verified file did not hit cache")
	}
	if scheme == repository.UpstreamAuthSchemeBasic && tokens.Load() == 0 {
		t.Fatal("Basic source login never happened")
	}
	if backend.reload != nil {
		store, backend.cache = backend.reload()
		handler = makeHandler()
		get(recipe, 200, true)
	}
	if backend.client != nil {
		backend.client(t, handler, name, "hot", assets)
	}
	beforeMissingKey := requests.Load() + tokens.Load()
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", "")
	get(recipe, 502, false)
	if requests.Load()+tokens.Load() != beforeMissingKey {
		t.Fatal("Conan missing key contacted source")
	}
	t.Setenv("GATEWAY_SETTINGS_ENCRYPTION_KEY", upstreamAuthTestKey)
	privateRejectSourceRebinding(t, handler, store, repo, func() int32 { return requests.Load() + tokens.Load() + fallbackCalls.Load() })
	beforeCorruption := requests.Load() + tokens.Load()
	repo = privateCorruptSourceCredential(t, store, repo, func() { get(recipe, 502, false) })
	if requests.Load()+tokens.Load() != beforeCorruption {
		t.Fatal("Conan damaged credential contacted source")
	}
	patch := func(secret string, clear bool) {
		t.Helper()
		auth := map[string]string{"scheme": string(scheme), "username": repo.UpstreamAuth.Username, "secret": secret}
		if clear {
			auth = map[string]string{"scheme": "none"}
		}
		body, _ := json.Marshal(map[string]any{"upstreamAuth": auth})
		r := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, bytes.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", repo.Version)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("Conan patch=%d %s", w.Code, w.Body.String())
		}
		repo, err = store.GetHostedRepository(t.Context(), repo.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	expected.Store(privateProxyAuthorization(scheme, "conan-source-second-secret"))
	patch("incorrect-source-secret", false)
	beforeFallback := fallbackCalls.Load()
	for i := 0; i < 4; i++ {
		get(root+"/revisions", 502, false)
		get(recipe, 502, false)
	}
	if fallbackCalls.Load() != beforeFallback {
		t.Fatal("Conan authentication failure reached anonymous fallback")
	}
	patch("conan-source-second-secret", false)
	get(root+"/revisions", 200, false)
	get(recipe, 200, true)
	refusalStatus.Store(http.StatusForbidden)
	patch("", true)
	beforeFallback = fallbackCalls.Load()
	for i := 0; i < 4; i++ {
		get(recipe, 502, false)
	}
	if fallbackCalls.Load() != beforeFallback {
		t.Fatal("Conan cleared credential reached anonymous fallback")
	}
	privateAssertRedactedManagementAndAudit(t, handler, store, repo, "conan-source-first-secret", "conan-source-second-secret", "incorrect-source-secret", "synthetic-conan-token-", "damaged-source-ciphertext")
}
