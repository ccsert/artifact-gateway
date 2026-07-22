package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: GiteaClient{}, Cache: NewConanCache(nil), Metrics: &Metrics{}}
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
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://gitea.example"}
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
	if audit.OccurredAt.IsZero() || audit.Format != "conan" || audit.Resource != "pkg/1.0/user/stable/revisions" || audit.Representation != "" || audit.MemberType != "hosted" || audit.UpstreamHost != "gitea.example" || audit.Operation != "get" || audit.Status != http.StatusOK || audit.CacheDisposition != "hit" || audit.Bytes == 0 || audit.RequestID != "conan-audit-request" || len(audit.TraceID) != 32 {
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
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://gitea.example"}
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

func (c *conanAcceptClient) FetchConan(_ context.Context, _ string, _ repository.Member, _ string, headers http.Header) (*http.Response, error) {
	c.calls++
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
		"/conan/v2/central/conans/pkg/1.0/user/stable/search",
	} {
		if _, _, _, _, ok := parseConanPath(http.MethodGet, path); ok {
			t.Fatalf("accepted %s", path)
		}
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
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://gitea.example"}
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
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://gitea.example"}
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

func TestConanSearchRouteDoesNotReachUpstream(t *testing.T) {
	store := repository.NewMemoryStore()
	member := repository.Member{Name: "hosted", Type: repository.MemberHosted, Endpoint: "test://hosted"}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	client := &conanAcceptClient{}
	w := httptest.NewRecorder()
	ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil)}.ServeHTTP(w, conanRequest("/conan/v2/central/conans/pkg/1.0/user/stable/revisions/rrev/search"))
	if w.Code != http.StatusNotFound || client.calls != 0 {
		t.Fatalf("status=%d calls=%d", w.Code, client.calls)
	}
	if len(store.Audits) == 0 || store.Audits[len(store.Audits)-1].Outcome != repository.AuditNotFound {
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

type conanStatusClient struct {
	status int
	body   string
}

func (c conanStatusClient) FetchConan(_ context.Context, _ string, _ repository.Member, _ string, _ http.Header) (*http.Response, error) {
	return &http.Response{StatusCode: c.status, Body: io.NopCloser(strings.NewReader(c.body)), Header: http.Header{}}, nil
}

func TestConanAnonymousReadRequiresPublicGroupAndMember(t *testing.T) {
	store := repository.NewMemoryStore()
	for _, group := range []repository.Group{
		{Name: "private", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}},
		{Name: "partial", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted}}},
		{Name: "public", Anonymous: true, Members: []repository.Member{{Name: "private", Type: repository.MemberHosted}, {Name: "public", Type: repository.MemberHosted, Anonymous: true}}},
	} {
		if _, err := store.CreateConanGroup(context.Background(), group); err != nil {
			t.Fatal(err)
		}
	}
	client := &conanAnonymousClient{}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	path := "/conan/v2/%s/conans/pkg/1.0/user/stable/revisions"
	for _, group := range []string{"private", "partial"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf(path, group), nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", group, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf(path, "public"), nil))
	if w.Code != http.StatusOK || client.member != "public" {
		t.Fatalf("response=%d member=%q", w.Code, client.member)
	}
}

func TestConanAnonymousReadDoesNotUsePrivateMemberCache(t *testing.T) {
	store := repository.NewMemoryStore()
	private := repository.Member{Name: "private", Type: repository.MemberHosted, Endpoint: "https://private.example"}
	public := repository.Member{Name: "public", Type: repository.MemberHosted, Endpoint: "https://public.example", Anonymous: true}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{private, public}})
	cache := NewConanCache(nil)
	path := "pkg/1.0/user/stable/revisions"
	if err := cache.store(context.Background(), cache.key("central", path, private), conanCacheEntry{body: []byte(`{"revisions":[{"revision":"private","time":1}]}`), contentType: "application/json", member: private.Name, endpoint: private.Endpoint}, "central", 1024, time.Minute); err != nil {
		t.Fatal(err)
	}
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: &conanAnonymousClient{}, Cache: cache, Metrics: &Metrics{}}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/conan/v2/central/conans/"+path, nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("response=%d body=%q", w.Code, w.Body.String())
	}
}

func TestConanAuthenticatedReadChecksMemberAuthorizationBeforeCache(t *testing.T) {
	store := repository.NewMemoryStore()
	private := repository.Member{Name: "private", Type: repository.MemberHosted, Endpoint: "https://private.example", Position: 0}
	public := repository.Member{Name: "public", Type: repository.MemberHosted, Endpoint: "https://public.example", Position: 1}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{private, public}})
	cache := NewConanCache(nil)
	path := "pkg/1.0/user/stable/revisions"
	if err := cache.store(context.Background(), cache.key("central", path, private), conanCacheEntry{body: []byte(`{"revisions":[]}`), contentType: "application/json", member: private.Name, endpoint: private.Endpoint}, "central", 1024, time.Minute); err != nil {
		t.Fatal(err)
	}
	auth := Authenticator{ResolverToken: "resolver-secret", RepositoryReaders: map[string][]string{"build-agent": {"central", "public"}}}
	w := httptest.NewRecorder()
	ConanHandler{Store: store, Authenticator: auth, Client: &conanAnonymousClient{}, Cache: cache}.ServeHTTP(w, conanRequest("/conan/v2/central/conans/"+path))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestConanProxyTLSE2EPinsVerifiedAddressAndUsesPersistentCache(t *testing.T) {
	var calls, lookups, dials atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/conans/pkg/1.0/user/stable/revisions" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"revisions":[{"revision":"rrev","time":1}]}`))
	}))
	defer upstream.Close()
	host, port := rawTLSServerAddress(t, upstream.URL)
	withRawProxyNetwork(t, func(_ context.Context, network, name string) ([]net.IP, error) {
		if lookups.Add(1) > 1 {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		if network != "ip" || name != "example.com" {
			t.Fatalf("lookup=%s %s", network, name)
		}
		return []net.IP{net.ParseIP("203.0.113.11")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		if address != net.JoinHostPort("203.0.113.11", port) {
			t.Fatalf("unverified dial=%q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
	})
	store := repository.NewMemoryStore()
	endpoint := "https://example.com:" + port
	member := repository.Member{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"example.com"}}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Members: []repository.Member{member}})
	objects := NewMemoryOCIObjectStore()
	first := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: GiteaClient{HTTPClient: upstream.Client()}, Cache: NewDefaultConanCache(objects, []string{"example.com"})}
	second := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: GiteaClient{HTTPClient: upstream.Client()}, Cache: NewDefaultConanCache(objects, []string{"example.com"})}
	path := "/conan/v2/central/conans/pkg/1.0/user/stable/revisions"
	for _, handler := range []ConanHandler{first, second} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, conanRequest(path))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 || lookups.Load() != 1 || dials.Load() != 1 {
		t.Fatalf("calls=%d lookups=%d dials=%d", calls.Load(), lookups.Load(), dials.Load())
	}
}

func TestConanProxyRedirectIsNotFollowed(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer target.Close()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer upstream.Close()
	host, port := rawTLSServerAddress(t, upstream.URL)
	withRawProxyNetwork(t, func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.12")}, nil
	}, func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("203.0.113.12", port) {
			t.Fatalf("dial=%q", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(host, port))
	})
	response, err := (GiteaClient{HTTPClient: upstream.Client()}).FetchConan(context.Background(), http.MethodGet, repository.Member{Type: repository.MemberProxy, Endpoint: "https://example.com:" + port}, "pkg/1.0/user/stable/revisions", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound || redirected.Load() != 0 {
		t.Fatalf("status=%d redirected=%d", response.StatusCode, redirected.Load())
	}
}

func TestConanProxyAllowlistIsolatedPerMember(t *testing.T) {
	store := repository.NewMemoryStore()
	endpoint := "https://shared.example"
	allowed := repository.Member{Name: "allowed", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"shared.example"}}
	denied := repository.Member{Name: "denied", Type: repository.MemberProxy, Endpoint: endpoint, AllowedHosts: []string{"other.example"}}
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "allowed-group", Members: []repository.Member{allowed}})
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "denied-group", Members: []repository.Member{denied}})
	client := &conanAnonymousClient{}
	for group, want := range map[string]int{"allowed-group": http.StatusOK, "denied-group": http.StatusForbidden} {
		w := httptest.NewRecorder()
		ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache([]string{"shared.example"})}.ServeHTTP(w, conanRequest("/conan/v2/"+group+"/conans/pkg/1.0/user/stable/revisions"))
		if w.Code != want {
			t.Fatalf("%s status=%d", group, w.Code)
		}
	}
}

func TestConan2ClientListsRevisionThroughGateway(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}})
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: &conanAnonymousClient{}, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	server := httptest.NewServer(h)
	defer server.Close()
	home := t.TempDir()
	run := func(args ...string) {
		command := exec.Command(conan, args...)
		command.Env = append(os.Environ(), "CONAN_HOME="+home)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("conan %v: %v\n%s", args, err, output)
		}
	}
	run("remote", "add", "--force", "gateway", server.URL+"/conan/central")
	run("list", "pkg/1.0@user/stable#*", "-r=gateway")
}

func TestConan2ClientDownloadsRevisionedRecipeThroughGateway(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}})
	client := newConanDownloadClient()
	h := ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil), Metrics: &Metrics{}}
	var gatewayPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gatewayPaths = append(gatewayPaths, r.URL.Path)
		h.ServeHTTP(w, r)
	}))
	defer server.Close()
	home := t.TempDir()
	run := func(args ...string) {
		command := exec.Command(conan, args...)
		command.Env = append(os.Environ(), "CONAN_HOME="+home)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("conan %v: %v\n%s\ngateway=%v upstream=%v", args, err, output, gatewayPaths, client.paths)
		}
	}
	run("remote", "add", "--force", "gateway", server.URL+"/conan/central")
	run("download", "pkg/1.0@user/stable#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "-r=gateway")
}

func TestConan2ClientDownloadsRevisionedPackageThroughGateway(t *testing.T) {
	conan := os.Getenv("CONAN_BINARY")
	if conan == "" {
		t.Skip("set CONAN_BINARY to run the Conan 2 client fixture")
	}
	store := repository.NewMemoryStore()
	_, _ = store.CreateConanGroup(context.Background(), repository.Group{Name: "central", Anonymous: true, Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Anonymous: true}}})
	client := newConanPackageClient()
	server := httptest.NewServer(ConanHandler{Store: store, Authenticator: testAuthenticator(), Client: client, Cache: NewConanCache(nil), Metrics: &Metrics{}})
	defer server.Close()
	home := t.TempDir()
	command := exec.Command(conan, "download", "pkg/1.0@user/stable#aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:package-id#bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "-r=gateway")
	command.Env = append(os.Environ(), "CONAN_HOME="+home)
	add := exec.Command(conan, "remote", "add", "--force", "gateway", server.URL+"/conan/central")
	add.Env = command.Env
	if output, err := add.CombinedOutput(); err != nil {
		t.Fatalf("add remote: %v\n%s", err, output)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("download package: %v\n%s\npaths=%v", err, output, client.paths)
	}
}

type conanPackageClient struct {
	*conanDownloadClient
	packageFiles map[string][]byte
}

func newConanPackageClient() *conanPackageClient {
	return &conanPackageClient{conanDownloadClient: newConanDownloadClient(), packageFiles: map[string][]byte{"conan_package.tgz": conanPackageArchive(), "conaninfo.txt": []byte("[settings]\n"), "conanmanifest.txt": []byte("manifest\n")}}
}
func conanPackageArchive() []byte {
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	_ = tarWriter.WriteHeader(&tar.Header{Name: "package.txt", Mode: 0644, Size: int64(len("package binary"))})
	_, _ = tarWriter.Write([]byte("package binary"))
	_ = tarWriter.Close()
	_ = gzipWriter.Close()
	return output.Bytes()
}
func (c *conanPackageClient) FetchConan(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	c.paths = append(c.paths, path)
	if strings.Contains(path, "/packages/package-id/") {
		if strings.HasSuffix(path, "/revisions") {
			return conanJSON(`{"revisions":[{"revision":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","time":1}]}`), nil
		}
		if strings.HasSuffix(path, "/files") {
			return conanFilesJSON(c.packageFiles), nil
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if body, ok := c.packageFiles[name]; ok {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		}
	}
	return c.conanDownloadClient.FetchConan(ctx, method, member, path, headers)
}
func conanFilesJSON(files map[string][]byte) *http.Response {
	values := make([]string, 0, len(files))
	for name, body := range files {
		sum := sha256.Sum256(body)
		values = append(values, fmt.Sprintf(`%q:{"sha256":%q,"size":%d}`, name, hex.EncodeToString(sum[:]), len(body)))
	}
	return conanJSON(`{"files":{` + strings.Join(values, ",") + `}}`)
}

type conanDownloadClient struct {
	files map[string][]byte
	paths []string
}

func newConanDownloadClient() *conanDownloadClient {
	return &conanDownloadClient{files: map[string][]byte{"conanfile.py": []byte("from conan import ConanFile\nclass Pkg(ConanFile):\n    name = 'pkg'\n    version = '1.0'\n"), "conanmanifest.txt": []byte("manifest\n")}}
}
func (c *conanDownloadClient) FetchConan(_ context.Context, _ string, _ repository.Member, path string, _ http.Header) (*http.Response, error) {
	c.paths = append(c.paths, path)
	if strings.HasSuffix(path, "/search") {
		return conanJSON(`{}`), nil
	}
	if strings.HasSuffix(path, "/revisions") {
		return conanJSON(`{"revisions":[{"revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","time":1}]}`), nil
	}
	if strings.HasSuffix(path, "/files") {
		files := make([]string, 0, len(c.files))
		for name, body := range c.files {
			sum := sha256.Sum256(body)
			files = append(files, fmt.Sprintf(`%q:{"sha256":%q,"size":%d}`, name, hex.EncodeToString(sum[:]), len(body)))
		}
		return conanJSON(`{"files":{` + strings.Join(files, ",") + `}}`), nil
	}
	name := path[strings.LastIndex(path, "/")+1:]
	if body, ok := c.files[name]; ok {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	}
	return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
}
func conanJSON(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

type conanAnonymousClient struct{ member string }

func (c *conanAnonymousClient) FetchConan(_ context.Context, _ string, member repository.Member, _ string, _ http.Header) (*http.Response, error) {
	c.member = member.Name
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"revisions":[{"revision":"rrev","time":1}]}`))}, nil
}
