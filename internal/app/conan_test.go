package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func conanRequest(path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer resolver-secret")
	return r
}

func TestConanHostedRecipeAndPackageFilesAreChecksumVerifiedAndCached(t *testing.T) {
	artifact := []byte("package archive")
	sum := sha256.Sum256(artifact)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/conans/pkg/1.0/user/stable/revisions/rrev/packages/package-id/revisions/prev/files" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"files":{"package.tgz":{"sha256":"` + hex.EncodeToString(sum[:]) + `","size":15}}}`))
			return
		}
		if r.URL.Path == "/conans/pkg/1.0/user/stable/revisions/rrev/packages/package-id/revisions/prev/files/package.tgz" {
			_, _ = w.Write(artifact)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: upstream.URL}}})
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: UpstreamClient{}, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/packages/package-id/revisions/prev/files/package.tgz"
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusOK || w.Body.String() != string(artifact) {
			t.Fatalf("response=%d %q", w.Code, w.Body.String())
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("upstream calls=%d, want file + metadata once", got)
	}
}

func TestConanChecksumMismatchIsNotCached(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://hosted"}}})
	client := &conanChecksumClient{}
	cache := NewConanCache(nil)
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: cache, Metrics: &Metrics{}}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/files/conanfile.py"
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusBadGateway {
			t.Fatalf("response=%d", w.Code)
		}
	}
	if client.fileCalls != 2 {
		t.Fatalf("bad artifact was cached: calls=%d", client.fileCalls)
	}
}

func TestConanAuditFieldsAndMetricsCoverCacheLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://legacy.example"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	metrics := &Metrics{}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: &conanAcceptClient{}, Cache: NewConanCache(nil), Metrics: metrics}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions"
	for range 2 {
		r := conanRequest(path)
		r.Header.Set("X-Request-ID", "conan-audit-request")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d", w.Code)
		}
	}
	if len(store.Audits) < 2 {
		t.Fatalf("audits=%#v", store.Audits)
	}
	audit := store.Audits[len(store.Audits)-1]
	if audit.OccurredAt.IsZero() || audit.Format != "conan" || audit.Resource != "pkg/1.0/user/stable/revisions" || audit.Representation != "" || audit.MemberType != "hosted" || audit.UpstreamHost != "legacy.example" || audit.Operation != "get" || audit.Status != http.StatusOK || audit.CacheDisposition != "hit" || audit.Bytes == 0 || audit.RequestID != "conan-audit-request" || len(audit.TraceID) != 32 {
		t.Fatalf("audit=%#v", audit)
	}
	metricsResponse := httptest.NewRecorder()
	metrics.Handler(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, line := range []string{"artifact_gateway_conan_requests_total{method=\"get\"} 2", "artifact_gateway_conan_cache_requests_total{outcome=\"hit\"} 1", "artifact_gateway_conan_cache_requests_total{outcome=\"miss\"} 1", "artifact_gateway_conan_response_bytes_total"} {
		if !strings.Contains(metricsResponse.Body.String(), line) {
			t.Fatalf("missing metric %q in %s", line, metricsResponse.Body.String())
		}
	}
	wantBytes := uint64(len(`{"revisions":[{"revision":"rrev","time":1}],"representation":""}`) * 2)
	if got := metrics.conanResponseBytes.Load(); got != wantBytes {
		t.Fatalf("Conan response bytes=%d, want exactly served bytes %d", got, wantBytes)
	}
}

type conanPublicationCoordinator struct {
	mu   sync.Mutex
	held bool
}

func (c *conanPublicationCoordinator) Acquire(context.Context, string, time.Duration) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.held {
		return "", false, nil
	}
	c.held = true
	return "owner", true, nil
}
func (c *conanPublicationCoordinator) Renew(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (c *conanPublicationCoordinator) Release(context.Context, string, string) error {
	c.mu.Lock()
	c.held = false
	c.mu.Unlock()
	return nil
}
func (c *conanPublicationCoordinator) CircuitOpen(context.Context, string) (bool, error) {
	return false, nil
}
func (c *conanPublicationCoordinator) OpenCircuit(context.Context, string, time.Duration) error {
	return nil
}
func (c *conanPublicationCoordinator) CloseCircuit(context.Context, string) error { return nil }

type conanInvalidationGateStore struct {
	OCIObjectStore
	listStarted chan struct{}
	allowList   chan struct{}
	once        sync.Once
}

func (s *conanInvalidationGateStore) List(ctx context.Context, prefix string) ([]string, error) {
	if prefix == "conan/index/" {
		s.once.Do(func() { close(s.listStarted) })
		select {
		case <-s.allowList:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.OCIObjectStore.List(ctx, prefix)
}

func TestConanInvalidationSharesPublicationLockAcrossInstances(t *testing.T) {
	base := NewMemoryOCIObjectStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://legacy.example"}
	seed := NewDefaultConanCache(base, nil)
	key := seed.key("central", "pkg/1.0/user/stable/revisions", member, "application/json")
	if err := seed.store(context.Background(), key, conanCacheEntry{body: []byte(`{"revisions":[]}`), contentType: "application/json", member: member.Name, endpoint: member.Endpoint, status: http.StatusOK}, "central", 0, time.Minute, "central", "pkg/1.0/user/stable/revisions", "application/json"); err != nil {
		t.Fatal(err)
	}
	store := &conanInvalidationGateStore{OCIObjectStore: base, listStarted: make(chan struct{}), allowList: make(chan struct{})}
	coordinator := &conanPublicationCoordinator{}
	cacheA := NewDefaultConanCache(store, nil).WithCoordinator(coordinator)
	cacheB := NewDefaultConanCache(store, nil).WithCoordinator(coordinator)

	invalidated := make(chan struct{})
	go func() {
		cacheA.Invalidate(context.Background(), "central", "pkg/1.0/user/stable/revisions", member)
		close(invalidated)
	}()
	select {
	case <-store.listStarted:
	case <-time.After(time.Second):
		t.Fatal("invalidation did not begin its index scan")
	}

	published := make(chan error, 1)
	go func() {
		published <- cacheB.store(context.Background(), cacheB.key("central", "pkg/1.0/user/stable/revisions", member, "text/plain"), conanCacheEntry{body: []byte("fresh"), contentType: "text/plain", member: member.Name, endpoint: member.Endpoint, status: http.StatusOK}, "central", 0, time.Minute, "central", "pkg/1.0/user/stable/revisions", "text/plain")
	}()
	select {
	case err := <-published:
		t.Fatalf("publication escaped concurrent invalidation: %v", err)
	case <-time.After(75 * time.Millisecond):
	}
	close(store.allowList)
	select {
	case <-invalidated:
	case <-time.After(time.Second):
		t.Fatal("invalidation did not complete")
	}
	if err := <-published; err != nil {
		t.Fatalf("publish after invalidation: %v", err)
	}
	if _, ok := cacheB.load(context.Background(), cacheB.key("central", "pkg/1.0/user/stable/revisions", member, "text/plain")); !ok {
		t.Fatal("post-invalidation publication was not readable")
	}
}

type conanChecksumClient struct{ fileCalls int }

func (c *conanChecksumClient) FetchConan(_ context.Context, _ string, _ repository.Member, path string, _ http.Header) (*http.Response, error) {
	if strings.HasSuffix(path, "/revisions") {
		return conanJSON(`{"revisions":[{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","time":"2024-01-01T00:00:00Z"}]}`), nil
	}
	if strings.HasSuffix(path, "/files") {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"files":{"conanfile.py":{"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":3}}}`)), Header: http.Header{}}, nil
	}
	c.fileCalls++
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("bad")), Header: http.Header{}}, nil
}

func TestConanHostedPrecedesProxyAndCachesNotFound(t *testing.T) {
	var hostedCalls, proxyCalls atomic.Int32
	hosted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hostedCalls.Add(1); http.NotFound(w, r) }))
	defer hosted.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		_, _ = w.Write([]byte(`{"revisions":[{"revision":"rrev","time":1}]}`))
	}))
	defer proxy.Close()
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://proxy.example", Position: 9, AllowedHosts: []string{"proxy.example"}}, {Name: "hosted", Type: repository.MemberHosted, Endpoint: hosted.URL, Position: 0}}})
	// Use a fixture client for Proxy to avoid requiring TLS while still proving
	// resolution order and the configured allowlist contract.
	client := conanFixtureClient{hostedURL: hosted.URL, proxyURL: proxy.URL}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: &client, Cache: NewConanCache([]string{"proxy.example"}), Metrics: &Metrics{}}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions"
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusOK {
			t.Fatalf("response=%d %s", w.Code, w.Body.String())
		}
	}
	if hostedCalls.Load() != 1 || proxyCalls.Load() != 1 {
		t.Fatalf("hosted=%d proxy=%d", hostedCalls.Load(), proxyCalls.Load())
	}

	missing := "/conan/v2/central/conans/pkg/2.0/user/stable/revisions"
	client.notFound = true
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(missing))
		if w.Code != http.StatusNotFound {
			t.Fatalf("missing=%d", w.Code)
		}
	}
	if client.calls != 4 {
		t.Fatalf("negative cache calls=%d", client.calls)
	}
}

func TestConanCacheSeparatesAcceptRepresentations(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://hosted"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	client := &conanAcceptClient{}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil)}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions"
	for _, accept := range []string{"application/a", "application/b", "application/a"} {
		r := conanRequest(path)
		r.Header.Set("Accept", accept)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), accept) {
			t.Fatalf("accept=%s status=%d body=%s", accept, w.Code, w.Body.String())
		}
	}
	if client.calls != 2 {
		t.Fatalf("upstream calls=%d", client.calls)
	}
}

type conanAcceptClient struct{ calls int }

func (c *conanAcceptClient) FetchConan(_ context.Context, _ string, _ repository.Member, path string, headers http.Header) (*http.Response, error) {
	c.calls++
	if strings.HasSuffix(path, "/search") {
		return conanJSON(`{"packages":{}}`), nil
	}
	accept := headers.Get("Accept")
	return conanJSON(`{"revisions":[{"revision":"rrev","time":1}],"representation":"` + accept + `"}`), nil
}

func TestConanRejectsOversizeMetadataAndArtifactWithoutCaching(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://hosted"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	objects := NewMemoryOCIObjectStore()
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: conanOversizeClient{}, Cache: NewDefaultConanCache(objects, nil).WithMaxObjectBytes(200)}
	for _, path := range []string{
		"/conan/v2/central/conans/pkg/1.0/user/stable/revisions",
		"/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/files/conanfile.py",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusBadGateway {
			t.Fatalf("path=%s status=%d", path, w.Code)
		}
	}
	keys, err := objects.List(context.Background(), "conan/")
	if err != nil || len(keys) != 0 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
}

type conanOversizeClient struct{}

func (conanOversizeClient) FetchConan(_ context.Context, _ string, _ repository.Member, path string, _ http.Header) (*http.Response, error) {
	if strings.HasSuffix(path, "/files") {
		return conanJSON(`{"files":{"conanfile.py":{"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":10}}}`), nil
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 300))), Header: http.Header{}}, nil
}

type conanFixtureClient struct {
	hostedURL, proxyURL string
	calls               int
	notFound            bool
}

func (c *conanFixtureClient) FetchConan(_ context.Context, _ string, member repository.Member, _ string, _ http.Header) (*http.Response, error) {
	c.calls++
	if c.notFound {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	}
	url := c.hostedURL
	if member.Type == repository.MemberProxy {
		url = c.proxyURL
	}
	return http.Get(url)
}

func TestConanRejectsMalformedAndUnsupportedRoutes(t *testing.T) {
	for _, path := range []string{
		"/conan/v2/central/conans/pkg/1.0/user/stable/revisions/%23bad/files",
		"/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/files/a%2fb",
	} {
		if _, _, _, _, ok := parseConanPath(http.MethodGet, path); ok {
			t.Fatalf("accepted %s", path)
		}
	}
}

func TestConanAllowsRevisionSearchNeededByConan2Download(t *testing.T) {
	if _, _, kind, _, ok := parseConanPath(http.MethodGet, "/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/search"); !ok || kind != "metadata" {
		t.Fatalf("revision search was rejected: ok=%t kind=%q", ok, kind)
	}
}

func TestConanLatestPackageRevisionIsValidatedAndUsesMetadataTTL(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://hosted"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	objects := NewMemoryOCIObjectStore()
	cache := NewDefaultConanCache(objects, nil)
	path := "pkg/1.0/user/stable/revisions/rrev/packages/package-id/latest"
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: conanStatusClient{status: http.StatusOK, body: `{"revision":"prev","time":"2024-01-01T00:00:00Z"}`}, Cache: cache}

	if _, _, kind, _, ok := parseConanPath(http.MethodGet, "/conan/v2/central/conans/"+path); !ok || kind != "metadata" {
		t.Fatalf("latest route: ok=%t kind=%q", ok, kind)
	}
	before := time.Now().UTC()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, conanRequest("/conan/v2/central/conans/"+path))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	encoded, err := objects.Get(context.Background(), cache.key("central", path, member, ""))
	if err != nil {
		t.Fatal(err)
	}
	var index conanCacheIndex
	if err := json.Unmarshal(encoded, &index); err != nil {
		t.Fatal(err)
	}
	if ttl := index.ExpiresAt.Sub(before); ttl < 55*time.Second || ttl > 65*time.Second {
		t.Fatalf("latest cache TTL=%s, want one minute", ttl)
	}

	badObjects := NewMemoryOCIObjectStore()
	bad := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: conanStatusClient{status: http.StatusOK, body: `{"revision":"prev"}`}, Cache: NewDefaultConanCache(badObjects, nil)}
	w = httptest.NewRecorder()
	bad.ServeHTTP(w, conanRequest("/conan/v2/central/conans/"+path))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("malformed latest status=%d body=%s", w.Code, w.Body.String())
	}
	if keys, err := badObjects.List(context.Background(), "conan/"); err != nil || len(keys) != 0 {
		t.Fatalf("malformed latest cached: keys=%v err=%v", keys, err)
	}
}

func TestConanAuthenticationHandshakeIsConan2GETOnly(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}})
	h := ConanHandler{Store: store, Authenticator: Authenticator{ResolverToken: "resolver-secret"}, Client: &conanAnonymousClient{}, Cache: NewConanCache(nil)}

	for _, request := range []struct {
		method, path string
	}{
		{http.MethodGet, "/conan/central/v1/users/authenticate"},
		{http.MethodPost, "/conan/central/v2/users/authenticate"},
		{http.MethodPut, "/conan/central/v2/users/authenticate"},
	} {
		t.Run(request.method+request.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(request.method, request.path, nil)
			r.SetBasicAuth("reader", "resolver-secret")
			h.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestConanSuccessfulHandshakesWriteResolvedAudits(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}})
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "authenticated", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}})
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: &conanAnonymousClient{}, Cache: NewConanCache(nil)}

	for _, test := range []struct {
		name, path, actor string
	}{
		{"anonymous ping", "/conan/public/v1/ping", "anonymous"},
		{"authenticated ping", "/conan/authenticated/v1/ping", "conan"},
		{"authenticated login", "/conan/authenticated/v2/users/authenticate", "conan"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.actor != "anonymous" {
				request.SetBasicAuth("conan", "resolver-secret")
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			audit := store.Audits[len(store.Audits)-1]
			if audit.GroupName == "" || audit.Actor != test.actor || audit.Outcome != repository.AuditResolved || audit.Operation != "get" || audit.Status != http.StatusOK || audit.CacheDisposition != "bypass" {
				t.Fatalf("audit=%#v", audit)
			}
		})
	}
}

func TestConanRejectsStringRevisionTimeWithoutCaching(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://hosted"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	objects := NewMemoryOCIObjectStore()
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: conanStatusClient{status: http.StatusOK, body: `{"revisions":[{"revision":"abc","time":"tomorrow"}]}`}, Cache: NewDefaultConanCache(objects, nil)}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, conanRequest("/conan/v2/central/conans/pkg/1.0/user/stable/revisions"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	keys, err := objects.List(context.Background(), "conan/")
	if err != nil || len(keys) != 0 {
		t.Fatalf("keys=%v err=%v", keys, err)
	}
}

func TestConanRejectsQuotedNumericRevisionTime(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://hosted"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: conanStatusClient{status: http.StatusOK, body: `{"revisions":[{"revision":"abc","time":"1"}]}`}, Cache: NewConanCache(nil)}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, conanRequest("/conan/v2/central/conans/pkg/1.0/user/stable/revisions"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestConanAuditsNonSuccessUpstreamStatus(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://legacy.example"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: conanStatusClient{status: http.StatusFound}, Cache: NewConanCache(nil)}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, conanRequest("/conan/v2/central/conans/pkg/1.0/user/stable/revisions"))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status=%d", w.Code)
	}
	if len(store.Audits) == 0 || store.Audits[len(store.Audits)-1].Outcome != repository.AuditUpstreamError || store.Audits[len(store.Audits)-1].Status != http.StatusFound {
		t.Fatalf("audits=%#v", store.Audits)
	}
}

func TestConanAuditRetainsSuccessfulUpstreamStatusAcrossCacheHit(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://legacy.example"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: conanStatusClient{status: http.StatusCreated, body: `{"revisions":[{"revision":"abc","time":1}]}`}, Cache: NewConanCache(nil)}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions"
	for range 2 {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d", w.Code)
		}
	}
	if len(store.Audits) != 2 {
		t.Fatalf("audits=%#v", store.Audits)
	}
	for _, audit := range store.Audits {
		if audit.Status != http.StatusCreated {
			t.Fatalf("audit=%#v", audit)
		}
	}
	if store.Audits[1].CacheDisposition != "hit" {
		t.Fatalf("cache disposition=%q", store.Audits[1].CacheDisposition)
	}
}

func TestConanRevisionSearchResolvesThroughUpstream(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://hosted"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	client := &conanAcceptClient{}
	w := httptest.NewRecorder()
	ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil)}.ServeHTTP(w, conanRequest("/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/search"))
	if w.Code != http.StatusOK || client.calls != 1 {
		t.Fatalf("status=%d calls=%d", w.Code, client.calls)
	}
	if len(store.Audits) == 0 || store.Audits[len(store.Audits)-1].Outcome != repository.AuditResolved {
		t.Fatalf("audits=%#v", store.Audits)
	}
}

func TestConanMalformedRouteReturnsBadRequestAndAudits(t *testing.T) {
	store := repository.NewMemoryStore()
	client := &conanAcceptClient{}
	w := httptest.NewRecorder()
	ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil)}.ServeHTTP(w, conanRequest("/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/files/a%2fb"))
	if w.Code != http.StatusBadRequest || client.calls != 0 {
		t.Fatalf("status=%d calls=%d", w.Code, client.calls)
	}
	if len(store.Audits) == 0 || store.Audits[len(store.Audits)-1].Outcome != repository.AuditUpstreamError || store.Audits[len(store.Audits)-1].Status != http.StatusBadRequest {
		t.Fatalf("audits=%#v", store.Audits)
	}
}

type conanAnonymousClient struct{ member string }

func (c *conanAnonymousClient) FetchConan(_ context.Context, _ string, member repository.Member, _ string, _ http.Header) (*http.Response, error) {
	c.member = member.Name
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"revisions":[{"revision":"rrev","time":"2024-01-01T00:00:00Z"}]}`))}, nil
}
