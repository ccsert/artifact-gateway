package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// proxyUpstream is a minimal Registry V2 / file upstream used by the native
// proxy repository tests. It records how many times each path was fetched so
// tests can assert the read-through cache serves repeat requests.
type proxyUpstream struct {
	mu      sync.Mutex
	bodies  map[string][]byte
	calls   map[string]int
	handler func(path string) (int, []byte)
}

func (u *proxyUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.calls[r.URL.Path]++
	u.mu.Unlock()
	if u.handler != nil {
		status, body := u.handler(r.URL.Path)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		u.writeBody(w, r.URL.Path, body)
		return
	}
	body, ok := u.bodies[r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	u.writeBody(w, r.URL.Path, body)
}

func (u *proxyUpstream) writeBody(w http.ResponseWriter, path string, body []byte) {
	sum := sha256.Sum256(body)
	w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	w.Header().Set("Docker-Content-Digest", "sha256:"+hex.EncodeToString(sum[:]))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (u *proxyUpstream) callCount(path string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls[path]
}

func TestNativeOCIProxyRepositoryPullsThroughUpstreamAndCaches(t *testing.T) {
	store := repository.NewMemoryStore()
	upstream := &proxyUpstream{bodies: map[string][]byte{}, calls: map[string]int{}}
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/app/manifests/latest" {
			upstream.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstreamServer.Close()
	upstream.bodies["/v2/app/manifests/latest"] = manifest

	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "proxy-oci", Name: "docker-proxy", Format: repository.FormatOCI,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	cache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithOCICache(Dependencies{}, store, TestAdapter{}, testAuthenticator(), cache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v2/docker-proxy/app/manifests/latest", nil)
		authorize(req, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := get()
	if first.Code != http.StatusOK || first.Body.String() != string(manifest) {
		t.Fatalf("first=%d body=%q", first.Code, first.Body.String())
	}
	if upstream.callCount("/v2/app/manifests/latest") != 1 {
		t.Fatalf("upstream calls=%d, want 1", upstream.callCount("/v2/app/manifests/latest"))
	}
	second := get()
	if second.Code != http.StatusOK || second.Body.String() != string(manifest) {
		t.Fatalf("second=%d body=%q", second.Code, second.Body.String())
	}
	if got := upstream.callCount("/v2/app/manifests/latest"); got != 1 {
		t.Fatalf("upstream calls after cache=%d, want 1 (cache hit)", got)
	}
}

func TestNativeOCIProxyRepositoryRejectsWrites(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "proxy-oci", Name: "docker-proxy", Format: repository.FormatOCI,
		Type: repository.RepositoryTypeProxy, Endpoint: "http://upstream.example", AllowedHosts: []string{"upstream.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	req := httptest.NewRequest(http.MethodPut, "/v2/docker-proxy/app/manifests/latest", strings.NewReader(`{}`))
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("put=%d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestNativeMavenProxyRepositoryPullsThroughUpstreamAndCaches(t *testing.T) {
	store := repository.NewMemoryStore()
	assetPath := "org/example/widget/1.0.0/widget-1.0.0.jar"
	upstream := &proxyUpstream{bodies: map[string][]byte{"/" + assetPath: []byte("jar-bytes")}, calls: map[string]int{}}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	allowedHost := strings.TrimPrefix(upstreamServer.URL, "http://")
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "proxy-maven", Name: "maven-proxy", Format: repository.FormatMaven,
		Type: repository.RepositoryTypeProxy, Endpoint: upstreamServer.URL, AllowedHosts: []string{allowedHost},
	})
	if err != nil {
		t.Fatal(err)
	}
	mavenCache := NewMavenCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, time.Hour, []string{allowedHost})
	handler := NewGatewayHandlerWithCaches(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), mavenCache, UpstreamClient{HTTPClient: upstreamServer.Client()})

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/maven/maven-proxy/"+assetPath, nil)
		authorize(req, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := get()
	if first.Code != http.StatusOK || first.Body.String() != "jar-bytes" {
		t.Fatalf("first=%d body=%q", first.Code, first.Body.String())
	}
	second := get()
	if second.Code != http.StatusOK || second.Body.String() != "jar-bytes" {
		t.Fatalf("second=%d body=%q", second.Code, second.Body.String())
	}
	if got := upstream.callCount("/" + assetPath); got != 1 {
		t.Fatalf("upstream calls=%d, want 1 (cache hit)", got)
	}
}

func TestNativeRawProxyRepositoryPullsThroughUpstreamAndCaches(t *testing.T) {
	store := repository.NewMemoryStore()
	// Raw applies HTTPS and host admission checks to proxy endpoints, so the
	// test uses a fixture client against a synthetic HTTPS endpoint rather than
	// a loopback server.
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "proxy-raw", Name: "raw-proxy", Format: repository.FormatRaw,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &rawFixtureClient{responses: map[string]int{"raw-proxy": http.StatusOK}, body: []byte("artifact")}
	rawCache := NewRawCache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, []string{"proxy.example"})
	handler := NewGatewayHandlerWithRawCache(Dependencies{}, store, TestAdapter{}, testAuthenticator(), NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), nil, rawCache, nil, client)

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/raw/raw-proxy/release/app.txt", nil)
		authorize(req, "resolver-secret")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	first := get()
	if first.Code != http.StatusOK || first.Body.String() != "artifact" {
		t.Fatalf("first=%d body=%q", first.Code, first.Body.String())
	}
	second := get()
	if second.Code != http.StatusOK || second.Body.String() != "artifact" {
		t.Fatalf("second=%d body=%q", second.Code, second.Body.String())
	}
	if got := client.Calls(); len(got) != 1 || got[0] != "raw-proxy" {
		t.Fatalf("upstream calls=%v, want a single fetch through the proxy member", got)
	}
}

func TestNativeConanProxyRepositoryPullsThroughUpstream(t *testing.T) {
	store := repository.NewMemoryStore()
	reference := "pkg/1.0/user/channel"
	revisionsBody := []byte(`{"revisions":[{"revision":"abc123","time":"2024-01-01T00:00:00Z"}]}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(revisionsBody)
	}))
	defer upstream.Close()
	// Conan applies HTTPS and host admission checks to proxy endpoints, so the
	// repository points at a synthetic HTTPS endpoint while the fixture client
	// serves the bytes from a loopback upstream.
	_, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "proxy-conan", Name: "conan-proxy", Format: repository.FormatConan,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://proxy.example", AllowedHosts: []string{"proxy.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &conanFixtureClient{proxyURL: upstream.URL}
	handler := ConanHandler{
		Store: store, NativeStore: store, Repositories: store,
		Authorizer:    RepositoryAuthorizer{Grants: store, Legacy: testAuthenticator()},
		Authenticator: testAuthenticator(), Client: client,
		Cache: NewConanCache(nil), NativeObjects: NewMemoryOCIObjectStore(), Metrics: &Metrics{},
	}
	req := httptest.NewRequest(http.MethodGet, "/conan/v2/conan-proxy/conans/"+reference+"/revisions", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != string(revisionsBody) {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
	if client.calls != 1 {
		t.Fatalf("upstream calls=%d, want 1", client.calls)
	}
}

func TestNativeProxyRepositoryUnknownFormatFallsThrough(t *testing.T) {
	store := repository.NewMemoryStore()
	// A hosted (non-proxy) repository must not be claimed by the proxy path.
	if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "hosted-oci", Name: "team", Format: repository.FormatOCI}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	req := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(req, "resolver-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("hosted repo manifest=%d, want %d (native hosted miss, not proxy)", w.Code, http.StatusNotFound)
	}
}
