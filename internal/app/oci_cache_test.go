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

	cache.RecordUpstreamFailure("https://registry.example")
	if cache.UpstreamAllowed("https://registry.example") {
		t.Fatal("open circuit allowed upstream request")
	}
	if cache.ProxyAllowed("https://untrusted.example") {
		t.Fatal("whitelist allowed untrusted proxy")
	}
}
