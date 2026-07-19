//go:build integration

package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type integrationOCIUpstream struct {
	mu        sync.Mutex
	responses map[string]integrationOCIResponse
	calls     map[string]int
	delay     time.Duration
}

type integrationOCIResponse struct {
	status  int
	content []byte
	digest  string
}

func (c *integrationOCIUpstream) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	parts := strings.Split(request.URL.Path, "/")
	reference := parts[len(parts)-1]
	c.mu.Lock()
	c.calls[reference]++
	response := c.responses[reference]
	c.mu.Unlock()

	digest := response.digest
	if digest == "" {
		digest = digestOf(response.content)
	}
	w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(response.status)
	_, _ = w.Write(response.content)
}

func (c *integrationOCIUpstream) Calls(reference string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[reference]
}

func TestOCIProxyCacheWithRedisAndS3AcrossGatewayInstances(t *testing.T) {
	redisAddress := os.Getenv("TEST_REDIS_ADDRESS")
	s3Endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if redisAddress == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("Redis and S3 integration environment is required")
	}

	const bucket = "oci-cache-integration"
	storeA, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeA.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	storeB, err := NewS3OCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}

	groupName := "proxy-cache-e2e"
	upstream := &integrationOCIUpstream{
		responses: map[string]integrationOCIResponse{
			"latest":  {status: http.StatusOK, content: []byte(`{"schemaVersion":2}`)},
			"missing": {status: http.StatusNotFound},
			"broken":  {status: http.StatusServiceUnavailable},
		},
		calls: make(map[string]int),
		delay: 20 * time.Millisecond,
	}
	upstreamServer := httptest.NewServer(upstream)
	defer upstreamServer.Close()
	endpoint := upstreamServer.URL
	allowedHost := strings.TrimPrefix(endpoint, "http://")
	repositoryStore := repository.NewMemoryStore()
	if _, err := repositoryStore.CreateGroup(context.Background(), repository.Group{Name: groupName, Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: endpoint, Position: 0}}}); err != nil {
		t.Fatal(err)
	}

	cacheA := NewOCICache(storeA, 40*time.Millisecond, 40*time.Millisecond, 40*time.Millisecond, []string{allowedHost}).WithCoordinator(NewRedisOCICacheCoordinator(redisAddress))
	cacheB := NewOCICache(storeB, 40*time.Millisecond, 40*time.Millisecond, 40*time.Millisecond, []string{allowedHost}).WithCoordinator(NewRedisOCICacheCoordinator(redisAddress))
	metricsA, metricsB := &Metrics{}, &Metrics{}
	client := GiteaClient{HTTPClient: upstreamServer.Client()}
	handlerA := OCIHandler{Resolver: Resolver{Store: repositoryStore, Adapter: TestAdapter{}, Metrics: metricsA}, Client: client, Authenticator: testAuthenticator(), Cache: cacheA}
	handlerB := OCIHandler{Resolver: Resolver{Store: repositoryStore, Adapter: TestAdapter{}, Metrics: metricsB}, Client: client, Authenticator: testAuthenticator(), Cache: cacheB}

	path := "/v2/" + groupName + "/app/manifests/latest"
	var wait sync.WaitGroup
	for _, handler := range []OCIHandler{handlerA, handlerB} {
		wait.Add(1)
		go func(handler OCIHandler) {
			defer wait.Done()
			response := integrationRequest(handler, http.MethodGet, path, "", "resolver-secret")
			if response.Code != http.StatusOK || response.Body.String() != `{"schemaVersion":2}` {
				t.Errorf("initial response = %d %q", response.Code, response.Body.String())
			}
		}(handler)
	}
	wait.Wait()
	if calls := upstream.Calls("latest"); calls != 1 {
		t.Fatalf("upstream latest calls = %d, want 1 across instances", calls)
	}

	upstream.mu.Lock()
	upstream.responses["latest"] = integrationOCIResponse{status: http.StatusServiceUnavailable}
	upstream.mu.Unlock()
	cached := integrationRequest(handlerB, http.MethodGet, path, "", "resolver-secret")
	if cached.Code != http.StatusOK || cached.Body.String() != `{"schemaVersion":2}` || upstream.Calls("latest") != 1 {
		t.Fatalf("offline cache response = %d %q calls=%d", cached.Code, cached.Body.String(), upstream.Calls("latest"))
	}

	missingPath := "/v2/" + groupName + "/app/manifests/missing"
	if response := integrationRequest(handlerA, http.MethodGet, missingPath, "", "resolver-secret"); response.Code != http.StatusNotFound {
		t.Fatalf("initial missing response = %d", response.Code)
	}
	if response := integrationRequest(handlerB, http.MethodGet, missingPath, "", "resolver-secret"); response.Code != http.StatusNotFound || upstream.Calls("missing") != 1 {
		t.Fatalf("negative cache response = %d calls=%d", response.Code, upstream.Calls("missing"))
	}

	brokenPath := "/v2/" + groupName + "/app/manifests/broken"
	if response := integrationRequest(handlerA, http.MethodGet, brokenPath, "", "resolver-secret"); response.Code != http.StatusBadGateway {
		t.Fatalf("initial upstream failure = %d", response.Code)
	}
	if response := integrationRequest(handlerB, http.MethodGet, brokenPath, "", "resolver-secret"); response.Code != http.StatusBadGateway || upstream.Calls("broken") != 1 {
		t.Fatalf("circuit breaker response = %d calls=%d", response.Code, upstream.Calls("broken"))
	}

	deniedGroup := groupName + "-denied"
	if _, err := repositoryStore.CreateGroup(context.Background(), repository.Group{Name: deniedGroup, Members: []repository.Member{{Name: "untrusted", Type: repository.MemberProxy, Endpoint: "https://untrusted.proxy-cache-e2e.test", Position: 0}}}); err != nil {
		t.Fatal(err)
	}
	if response := integrationRequest(handlerA, http.MethodGet, "/v2/"+deniedGroup+"/app/manifests/latest", "", "resolver-secret"); response.Code != http.StatusForbidden {
		t.Fatalf("whitelist response = %d", response.Code)
	}
	if upstream.Calls("latest") != 1 {
		t.Fatalf("whitelist denial made upstream request")
	}

	metrics := httptest.NewRecorder()
	metricsB.Handler(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, want := range []string{
		`artifact_gateway_oci_cache_requests_total{outcome="hit"} 2`,
		"artifact_gateway_oci_negative_cache_hits_total 1",
		"artifact_gateway_oci_upstream_circuit_open_total 1",
	} {
		if !strings.Contains(metrics.Body.String(), want) {
			t.Fatalf("metrics missing %q:\n%s", want, metrics.Body.String())
		}
	}
	deniedMetrics := httptest.NewRecorder()
	metricsA.Handler(deniedMetrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(deniedMetrics.Body.String(), "artifact_gateway_oci_proxy_denied_total 1") {
		t.Fatalf("whitelist metric missing:\n%s", deniedMetrics.Body.String())
	}

	time.Sleep(60 * time.Millisecond)
	upstream.mu.Lock()
	upstream.responses["latest"] = integrationOCIResponse{status: http.StatusOK, content: []byte(`{"schemaVersion":3}`)}
	upstream.responses["missing"] = integrationOCIResponse{status: http.StatusOK, content: []byte(`{"schemaVersion":4}`)}
	upstream.responses["broken"] = integrationOCIResponse{status: http.StatusOK, content: []byte(`{"schemaVersion":5}`)}
	upstream.responses["invalid"] = integrationOCIResponse{status: http.StatusOK, content: []byte("invalid"), digest: "sha256:0000000000000000000000000000000000000000000000000000000000000000"}
	upstream.mu.Unlock()
	if response := integrationRequest(handlerA, http.MethodGet, path, "", "resolver-secret"); response.Code != http.StatusOK || response.Body.String() != `{"schemaVersion":3}` || upstream.Calls("latest") != 2 {
		t.Fatalf("cache TTL response = %d %q calls=%d", response.Code, response.Body.String(), upstream.Calls("latest"))
	}
	if response := integrationRequest(handlerB, http.MethodGet, missingPath, "", "resolver-secret"); response.Code != http.StatusOK || response.Body.String() != `{"schemaVersion":4}` || upstream.Calls("missing") != 2 {
		t.Fatalf("negative TTL response = %d %q calls=%d", response.Code, response.Body.String(), upstream.Calls("missing"))
	}
	if response := integrationRequest(handlerB, http.MethodGet, brokenPath, "", "resolver-secret"); response.Code != http.StatusOK || response.Body.String() != `{"schemaVersion":5}` || upstream.Calls("broken") != 2 {
		t.Fatalf("circuit TTL response = %d %q calls=%d", response.Code, response.Body.String(), upstream.Calls("broken"))
	}

	invalidPath := "/v2/" + groupName + "/app/blobs/invalid"
	if response := integrationRequest(handlerA, http.MethodGet, invalidPath, "", "resolver-secret"); response.Code != http.StatusBadGateway {
		t.Fatalf("digest mismatch response = %d", response.Code)
	}
	invalidKey := cacheA.key(groupName, groupName+"/app", ociBlob, "invalid")
	if _, err := storeA.Get(context.Background(), invalidKey); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("invalid digest published cache index: %v", err)
	}
	upstream.mu.Lock()
	upstream.responses["invalid"] = integrationOCIResponse{status: http.StatusOK, content: []byte("valid")}
	upstream.mu.Unlock()
	time.Sleep(60 * time.Millisecond)
	if response := integrationRequest(handlerB, http.MethodGet, invalidPath, "", "resolver-secret"); response.Code != http.StatusOK || response.Body.String() != "valid" || upstream.Calls("invalid") != 2 {
		t.Fatalf("digest recovery response = %d %q calls=%d", response.Code, response.Body.String(), upstream.Calls("invalid"))
	}
	upstream.mu.Lock()
	upstream.responses["invalid"] = integrationOCIResponse{status: http.StatusServiceUnavailable}
	upstream.mu.Unlock()
	if response := integrationRequest(handlerA, http.MethodGet, invalidPath, "", "resolver-secret"); response.Code != http.StatusOK || response.Body.String() != "valid" || upstream.Calls("invalid") != 2 {
		t.Fatalf("offline blob response = %d %q calls=%d", response.Code, response.Body.String(), upstream.Calls("invalid"))
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, invalidPath, nil)
	rangeRequest.Header.Set("Range", "bytes=1-3")
	authorize(rangeRequest, "resolver-secret")
	ranged := httptest.NewRecorder()
	handlerA.ServeHTTP(ranged, rangeRequest)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "ali" || upstream.Calls("invalid") != 2 {
		t.Fatalf("offline blob range = %d %q calls=%d", ranged.Code, ranged.Body.String(), upstream.Calls("invalid"))
	}
}
