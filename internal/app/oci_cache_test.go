package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type countingOCIClient struct {
	mu      sync.Mutex
	calls   int
	content []byte
	status  int
	delay   time.Duration
}

type renewingTestCoordinator struct {
	mu       sync.Mutex
	renewals int
	fail     bool
}

func (c *renewingTestCoordinator) Acquire(context.Context, string, time.Duration) (string, bool, error) {
	return "owner", true, nil
}

func (c *renewingTestCoordinator) Renew(context.Context, string, string, time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renewals++
	return !c.fail, nil
}

func (c *renewingTestCoordinator) Release(context.Context, string, string) error { return nil }
func (c *renewingTestCoordinator) CircuitOpen(context.Context, string) (bool, error) {
	return false, nil
}
func (c *renewingTestCoordinator) OpenCircuit(context.Context, string, time.Duration) error {
	return nil
}
func (c *renewingTestCoordinator) CloseCircuit(context.Context, string) error { return nil }

func (c *renewingTestCoordinator) Renewals() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renewals
}

func TestOCICacheRenewsDistributedLockAndCancelsLostOwner(t *testing.T) {
	coordinator := &renewingTestCoordinator{}
	cache := NewDefaultOCICache(NewMemoryOCIObjectStore(), nil).WithCoordinator(coordinator)
	cache.lockLease = 30 * time.Millisecond
	cache.lockRenewEvery = 5 * time.Millisecond

	content, err := cache.Do(context.Background(), "key", func(ctx context.Context) (CachedOCIContent, error) {
		select {
		case <-time.After(16 * time.Millisecond):
			return CachedOCIContent{Body: []byte("ok")}, nil
		case <-ctx.Done():
			return CachedOCIContent{}, ctx.Err()
		}
	})
	if err != nil || string(content.Body) != "ok" || coordinator.Renewals() < 2 {
		t.Fatalf("content=%q err=%v renewals=%d", content.Body, err, coordinator.Renewals())
	}

	coordinator.fail = true
	_, err = cache.Do(context.Background(), "lost-owner", func(ctx context.Context) (CachedOCIContent, error) {
		<-ctx.Done()
		return CachedOCIContent{}, ctx.Err()
	})
	if err == nil || !strings.Contains(err.Error(), "renewal failed") {
		t.Fatalf("error = %v, want renewal failure", err)
	}
}

func (c *countingOCIClient) Fetch(_ context.Context, method string, _ repository.Member, _, _, _ string, _ http.Header) (*http.Response, error) {
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	digest := digestOf(c.content)
	return &http.Response{StatusCode: c.status, Header: http.Header{"Docker-Content-Digest": []string{digest}, "Content-Type": []string{"application/vnd.oci.image.manifest.v1+json"}}, Body: io.NopCloser(bytes.NewReader(c.content)), Request: &http.Request{Method: method}}, nil
}

func (c *countingOCIClient) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestOCICacheServesVerifiedContentAndCoalescesConcurrentPulls(t *testing.T) {
	content := []byte(`{"schemaVersion":2}`)
	client := &countingOCIClient{content: content, status: http.StatusOK, delay: 20 * time.Millisecond}
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://registry.example", Position: 0}}})
	cache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Minute, time.Minute, []string{"registry.example"})
	metrics := &Metrics{}
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: metrics}, Client: client, Authenticator: testAuthenticator(), Cache: cache}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
			authorize(req, "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusOK || response.Body.String() != string(content) {
				t.Errorf("response = %d %q", response.Code, response.Body.String())
			}
		}()
	}
	wg.Wait()
	if got := client.Calls(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}

	client.status = http.StatusServiceUnavailable
	req := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(req, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || response.Body.String() != string(content) || client.Calls() != 1 {
		t.Fatalf("cached response = %d %q; upstream calls = %d", response.Code, response.Body.String(), client.Calls())
	}
	metricResponse := httptest.NewRecorder()
	metrics.Handler(metricResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metricResponse.Body.String(), `artifact_gateway_oci_cache_requests_total{outcome="hit"} 1`) {
		t.Fatalf("metrics = %s", metricResponse.Body.String())
	}
}

func TestOCICacheStreamsLargeBlobAndServesCachedRange(t *testing.T) {
	content := bytes.Repeat([]byte("0123456789abcdef"), 256*1024) // 4 MiB
	client := &countingOCIClient{content: content, status: http.StatusOK, delay: 20 * time.Millisecond}
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://registry.example", Position: 0}}})
	cache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Minute, time.Minute, []string{"registry.example"})
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: &Metrics{}}, Client: client, Authenticator: testAuthenticator(), Cache: cache}
	path := "/v2/team/app/blobs/" + digestOf(content)

	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			authorize(req, "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusOK || response.Body.Len() != len(content) {
				t.Errorf("response = %d length=%d", response.Code, response.Body.Len())
			}
		}()
	}
	wg.Wait()
	if got := client.Calls(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}

	client.status = http.StatusServiceUnavailable
	rangeRequest := httptest.NewRequest(http.MethodGet, path, nil)
	rangeRequest.Header.Set("Range", "bytes=1048576-1048591")
	authorize(rangeRequest, "resolver-secret")
	ranged := httptest.NewRecorder()
	handler.ServeHTTP(ranged, rangeRequest)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != string(content[1048576:1048592]) || ranged.Header().Get("Content-Range") != "bytes 1048576-1048591/"+utoa(uint64(len(content))) {
		t.Fatalf("range = %d headers=%v length=%d", ranged.Code, ranged.Header(), ranged.Body.Len())
	}
	if got := client.Calls(); got != 1 {
		t.Fatalf("cached range made upstream calls = %d", got)
	}
}

func TestOCIHostedSingleflightGivesEveryWaiterAnIndependentReader(t *testing.T) {
	content := bytes.Repeat([]byte("hosted"), 1024)
	client := &countingOCIClient{content: content, status: http.StatusOK, delay: 20 * time.Millisecond}
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "hosted", Type: repository.MemberHosted, Endpoint: "https://hosted.example", Position: 0}}})
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: &Metrics{}}, Client: client, Authenticator: testAuthenticator(), Cache: NewDefaultOCICache(NewMemoryOCIObjectStore(), nil)}

	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/v2/team/app/blobs/"+digestOf(content), nil)
			authorize(req, "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusOK || response.Body.Len() != len(content) {
				t.Errorf("response = %d length=%d", response.Code, response.Body.Len())
			}
		}()
	}
	wg.Wait()
	if got := client.Calls(); got != 1 {
		t.Fatalf("upstream calls = %d, want 1", got)
	}
}

func TestOCICacheExpiresAndProxyPolicyIsEnforcedInRequestPath(t *testing.T) {
	cache := NewOCICache(NewMemoryOCIObjectStore(), -time.Millisecond, time.Hour, time.Hour, []string{"trusted.example"})
	key := cache.key("team", "team/app", ociManifest, "latest")
	if err := cache.Store(context.Background(), key, CachedOCIContent{Body: []byte("content"), Digest: digestOf([]byte("content"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(context.Background(), key); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("expired cache error = %v, want miss", err)
	}

	client := &countingOCIClient{content: []byte(`{"schemaVersion":2}`), status: http.StatusOK}
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "untrusted", Type: repository.MemberProxy, Endpoint: "https://untrusted.example", Position: 0}}})
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: &Metrics{}}, Client: client, Authenticator: testAuthenticator(), Cache: cache}
	req := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
	authorize(req, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound || client.Calls() != 0 {
		t.Fatalf("untrusted proxy response = %d, calls = %d", response.Code, client.Calls())
	}
}

func TestOCICacheCollectsOnlyUnreferencedDigestObjectsAfterGracePeriod(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewOCICache(store, time.Hour, time.Hour, time.Hour, nil)
	cache.gcGrace = 5 * time.Millisecond
	first := []byte("first")
	second := []byte("second")
	firstObject := "oci/objects/" + strings.ReplaceAll(digestOf(first), ":", "/")
	keyA := cache.key("team", "team/a", ociManifest, "latest")
	keyB := cache.key("team", "team/b", ociManifest, "latest")
	if err := cache.Store(context.Background(), keyA, CachedOCIContent{Body: first, Digest: digestOf(first)}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), keyB, CachedOCIContent{Body: first, Digest: digestOf(first)}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(context.Background(), keyA, CachedOCIContent{Body: second, Digest: digestOf(second)}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := cache.CollectGarbage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), firstObject); err != nil {
		t.Fatalf("shared digest was removed: %v", err)
	}
	if err := cache.Store(context.Background(), keyB, CachedOCIContent{Body: second, Digest: digestOf(second)}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := cache.CollectGarbage(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), firstObject); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("orphan digest error = %v, want miss", err)
	}
}

func TestOCICacheNegativeResultAndCircuitBreakerAvoidRepeatedProxyRequests(t *testing.T) {
	client := &countingOCIClient{content: []byte("missing"), status: http.StatusNotFound}
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://registry.example", Position: 0}}})
	cache := NewOCICache(NewMemoryOCIObjectStore(), time.Hour, time.Hour, time.Hour, []string{"registry.example"})
	handler := OCIHandler{Resolver: Resolver{Store: store, Adapter: TestAdapter{}, Metrics: &Metrics{}}, Client: client, Authenticator: testAuthenticator(), Cache: cache}
	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/missing", nil)
		authorize(req, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusNotFound {
			t.Fatalf("response = %d", response.Code)
		}
	}
	if got := client.Calls(); got != 1 {
		t.Fatalf("negative cache upstream calls = %d, want 1", got)
	}

	cache.RecordUpstreamFailure(context.Background(), "https://registry.example")
	if cache.UpstreamAllowed(context.Background(), "https://registry.example") {
		t.Fatal("open circuit allowed upstream request")
	}
	if cache.ProxyAllowed("https://untrusted.example") {
		t.Fatal("whitelist allowed untrusted proxy")
	}
}
