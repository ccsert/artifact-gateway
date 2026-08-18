//go:build integration

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type countingDeleteObjectStore struct {
	objectstore.Store
	mu      sync.Mutex
	deletes map[string]int
}

type failingLegacyIndexStore struct {
	OCIObjectStore
	key string
	err error
}

func (s failingLegacyIndexStore) Get(ctx context.Context, key string) ([]byte, error) {
	if key == s.key {
		return nil, s.err
	}
	return s.OCIObjectStore.Get(ctx, key)
}

func (s *countingDeleteObjectStore) Delete(ctx context.Context, key string) error {
	s.mu.Lock()
	if s.deletes == nil {
		s.deletes = make(map[string]int)
	}
	s.deletes[key]++
	s.mu.Unlock()
	return s.Store.Delete(ctx, key)
}

func (s *countingDeleteObjectStore) deleteCount(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deletes[key]
}

func TestPostgresCacheCoordinatorCloseReleasesBorrowedAdvisoryLocks(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PostgreSQL integration environment is required")
	}
	lockConfig := database.DefaultCoordinatorPoolConfig()
	lockConfig.MaxOpenConns = 1
	lockConfig.MaxIdleConns = 1
	lockDB, err := database.OpenPostgres(databaseURL, lockConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lockDB.Close() }()
	stateDB, err := database.OpenPostgres(databaseURL, database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stateDB.Close() }()
	coordinator, err := NewPostgresCacheCoordinatorWithPools(lockDB, stateDB)
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("close-release-%d", time.Now().UnixNano())
	if _, acquired, err := coordinator.Acquire(context.Background(), key, time.Minute); err != nil || !acquired {
		t.Fatalf("Acquire() acquired=%t err=%v", acquired, err)
	}
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	verifier, err := database.OpenPostgres(databaseURL, lockConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = verifier.Close() }()
	lockKey := "artifact-gateway:cache:" + key
	var acquired bool
	if err := verifier.QueryRow(`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, lockKey).Scan(&acquired); err != nil || !acquired {
		t.Fatalf("advisory lock remained after Close(): acquired=%t err=%v", acquired, err)
	}
	if _, err := verifier.Exec(`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockKey); err != nil {
		t.Fatal(err)
	}
}

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

// rawPublicationGateStore pauses a Raw index write after its object has been
// staged, making the store/collector race deterministic across cache instances.
type rawPublicationGateStore struct {
	OCIObjectStore
	indexKey     string
	objectStored chan struct{}
	allowIndex   chan struct{}
	once         sync.Once
}

func (s *rawPublicationGateStore) Put(ctx context.Context, key string, value []byte) error {
	if strings.HasPrefix(key, "raw/objects/") {
		if err := s.OCIObjectStore.Put(ctx, key, value); err != nil {
			return err
		}
		s.once.Do(func() { close(s.objectStored) })
		return nil
	}
	if key == s.indexKey {
		select {
		case <-s.allowIndex:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.OCIObjectStore.Put(ctx, key, value)
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

func TestPostgresCacheControlStoreKeepsAllFormatIndexesOutOfS3(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}

	bucket := fmt.Sprintf("cache-control-%d", time.Now().UnixNano())
	objects, err := NewRustFSOCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	control, err := NewPostgresCacheControlStore(objects, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	for _, key := range []string{
		"oci/index/control.json", "oci/gc/control.json", "maven/index/control.json", "raw/index/control.json", "conan/index/control.json",
	} {
		if err := control.Put(context.Background(), key, []byte(`{"expires_at":"2099-01-01T00:00:00Z"}`)); err != nil {
			t.Fatalf("store %s: %v", key, err)
		}
		if _, err := control.Get(context.Background(), key); err != nil {
			t.Fatalf("load %s: %v", key, err)
		}
		if objects, err := objects.List(context.Background(), key[:strings.LastIndex(key, "/")+1]); err != nil || len(objects) != 0 {
			t.Fatalf("S3 control objects for %s = %#v, %v; want none", key, objects, err)
		}
	}
	if err := control.Put(context.Background(), "oci/objects/bytes", []byte("artifact bytes")); err != nil {
		t.Fatal(err)
	}
	if objects, err := objects.List(context.Background(), "oci/objects/"); err != nil || len(objects) != 1 {
		t.Fatalf("S3 artifact objects = %#v, %v; want one", objects, err)
	}
}

func TestPostgresCacheControlStoreReadsAndMigratesLegacyS3IndexesWithoutDeletingThem(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}

	ctx := context.Background()
	bucket := fmt.Sprintf("legacy-cache-control-%d", time.Now().UnixNano())
	objects, err := NewRustFSOCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	control, err := NewPostgresCacheControlStore(objects, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()

	prefix := fmt.Sprintf("maven/index/legacy-%d-", time.Now().UnixNano())
	keys := []string{prefix + "a.json", prefix + "b.json"}
	values := [][]byte{[]byte(`{"object":"maven/objects/a"}`), []byte(`{"object":"maven/objects/b"}`)}
	for index, key := range keys {
		if err := objects.Put(ctx, key, values[index]); err != nil {
			t.Fatalf("store legacy S3 index %s: %v", key, err)
		}
	}

	loaded, err := control.Get(ctx, keys[0])
	if err != nil || string(loaded) != string(values[0]) {
		t.Fatalf("load legacy S3 index = %q, %v", loaded, err)
	}
	var migrated string
	if err := control.db.QueryRowContext(ctx, `SELECT value::text FROM cache_control_entries WHERE key=$1`, keys[0]).Scan(&migrated); err != nil {
		t.Fatalf("legacy index was not lazily migrated to PostgreSQL: %v", err)
	}
	if _, err := objects.Get(ctx, keys[0]); err != nil {
		t.Fatalf("legacy S3 index must remain available for binary rollback: %v", err)
	}

	listed, err := control.List(ctx, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0] != keys[0] || listed[1] != keys[1] {
		t.Fatalf("legacy and migrated cache indexes = %#v; want %#v", listed, keys)
	}
	if err := control.Delete(ctx, keys[0]); err != nil {
		t.Fatalf("delete migrated legacy index: %v", err)
	}
	if _, err := control.Get(ctx, keys[0]); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("deleted legacy index was resurrected: %v", err)
	}
	if _, err := objects.Get(ctx, keys[0]); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("legacy S3 index remained visible to rollback binary: %v", err)
	}
	listed, err = control.List(ctx, prefix)
	if err != nil || len(listed) != 1 || listed[0] != keys[1] {
		t.Fatalf("indexes after deletion = %#v, %v; want %#v", listed, err, keys[1:])
	}

	infrastructureErr := errors.New("RustFS authorization failed")
	failingKey := prefix + "infrastructure-error.json"
	control.objects = failingLegacyIndexStore{OCIObjectStore: objects, key: failingKey, err: infrastructureErr}
	if _, err := control.Get(ctx, failingKey); !errors.Is(err, infrastructureErr) {
		t.Fatalf("legacy S3 infrastructure error was reduced to a cache miss: %v", err)
	}
}

func TestPostgresCacheTaskQueueClaimsWorkOnceAcrossGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	queueA, err := NewPostgresCacheTaskQueue(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer queueA.Close()
	queueB, err := NewPostgresCacheTaskQueue(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer queueB.Close()
	if err := queueA.EnqueueCollectionForFormat(context.Background(), cacheCollectionFormatOCI); err != nil {
		t.Fatal(err)
	}
	if err := queueA.EnqueueCollectionForFormat(context.Background(), cacheCollectionFormatRaw); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := queueA.claimCollection(context.Background(), cacheCollectionFormatOCI, time.Minute)
	if err != nil || !ok {
		t.Fatalf("first claim = %#v, %t, %v", claimed, ok, err)
	}
	if _, ok, err := queueB.claimCollection(context.Background(), cacheCollectionFormatOCI, time.Minute); err != nil || ok {
		t.Fatalf("second claim while leased = %t, %v; want unavailable", ok, err)
	}
	rawClaimed, ok, err := queueB.claimCollection(context.Background(), cacheCollectionFormatRaw, time.Minute)
	if err != nil || !ok {
		t.Fatalf("different-format claim = %#v, %t, %v", rawClaimed, ok, err)
	}
	if err := queueA.complete(context.Background(), claimed); err != nil {
		t.Fatal(err)
	}
	if err := queueB.complete(context.Background(), rawClaimed); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresCacheWorkerLeavesUnconfiguredFormatsUnclaimed(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	queue, err := NewPostgresCacheTaskQueue(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	if err = queue.EnqueueCollectionForFormat(context.Background(), cacheCollectionFormatOCI); err != nil {
		t.Fatal(err)
	}
	if err = queue.EnqueueCollectionForFormat(context.Background(), cacheCollectionFormatRaw); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	collected := make(chan string, 1)
	queue.StartCacheWorker(ctx, []string{cacheCollectionFormatRaw}, func(_ context.Context, format string) error {
		collected <- format
		return nil
	})
	select {
	case format := <-collected:
		if format != cacheCollectionFormatRaw {
			t.Fatalf("collected format=%q want=%q", format, cacheCollectionFormatRaw)
		}
	case <-time.After(time.Second):
		t.Fatal("Raw cache worker did not collect its configured format")
	}

	deadline := time.Now().Add(time.Second)
	for {
		var remaining int
		err = queue.db.QueryRowContext(context.Background(), `SELECT count(*) FROM cache_tasks WHERE task_type=$1 AND completed_at IS NULL`, cacheCollectionTaskPrefix+cacheCollectionFormatRaw).Scan(&remaining)
		if err != nil {
			t.Fatal(err)
		}
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Raw cache worker did not complete its claimed task")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	ociTask, claimed, err := queue.claimCollection(context.Background(), cacheCollectionFormatOCI, time.Minute)
	if err != nil || !claimed {
		t.Fatalf("OCI task after Raw worker=%#v claimed=%t err=%v", ociTask, claimed, err)
	}
	if err = queue.complete(context.Background(), ociTask); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresNativeOCIReclaimLeasePreventsDuplicateDeletion(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	storeA, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeA.Close()
	storeB, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer storeB.Close()
	repo, err := storeA.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "reclaim-lease-" + uuid.NewString(), Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	upload := repository.OCIUpload{ID: uuid.NewString(), RepositoryID: repo.ID, Name: "team/app", ObjectKey: "native/oci/uploads/" + uuid.NewString(), State: "open", ExpiresAt: now.Add(-time.Minute)}
	if _, err = storeA.CreateOCIUpload(ctx, upload); err != nil {
		t.Fatal(err)
	}
	objects := &countingDeleteObjectStore{Store: NewMemoryOCIObjectStore()}
	if err = objects.Put(ctx, upload.ObjectKey, []byte("partial")); err != nil {
		t.Fatal(err)
	}
	maintenanceA := NativeOCIMaintenance{Store: storeA, Objects: objects, Now: func() time.Time { return now }}
	if err = maintenanceA.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	maintenanceB := NativeOCIMaintenance{Store: storeB, Objects: objects, Now: func() time.Time { return now }}
	results := make(chan error, 2)
	go func() { results <- maintenanceA.RunReclaimJobs(ctx, 10) }()
	go func() { results <- maintenanceB.RunReclaimJobs(ctx, 10) }()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := objects.deleteCount(upload.ObjectKey); got != 1 {
		t.Fatalf("target OCI upload deleted %d times, want 1", got)
	}
	state, err := storeA.GetOCIUpload(ctx, upload.ID)
	if err != nil || state.CollectedAt.IsZero() {
		t.Fatalf("collected upload=%#v err=%v", state, err)
	}
}

func TestOCIProxyCacheWithPostgresAndS3AcrossGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}

	const bucket = "oci-cache-integration"
	storeA, err := NewRustFSOCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeA.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	storeB, err := NewRustFSOCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	controlA, err := NewPostgresCacheControlStore(storeA, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer controlA.Close()
	controlB, err := NewPostgresCacheControlStore(storeB, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer controlB.Close()

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

	coordinatorA, err := NewPostgresCacheCoordinator(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinatorA.Close()
	coordinatorB, err := NewPostgresCacheCoordinator(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinatorB.Close()
	// Keep the cache window comfortably above a clean runner's PostgreSQL/S3
	// round-trip. A 40 ms TTL made the cross-instance negative-cache assertion
	// depend on host scheduling rather than the cache contract under test.
	const (
		cacheTTL   = 2 * time.Second
		breakerTTL = time.Second
	)
	cacheA := NewOCICache(controlA, cacheTTL, cacheTTL, breakerTTL, []string{allowedHost}).WithCoordinator(coordinatorA)
	cacheB := NewOCICache(controlB, cacheTTL, cacheTTL, breakerTTL, []string{allowedHost}).WithCoordinator(coordinatorB)
	metricsA, metricsB := &Metrics{}, &Metrics{}
	client := UpstreamClient{HTTPClient: upstreamServer.Client()}
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
	if indexes, err := storeA.List(context.Background(), "oci/index/"); err != nil || len(indexes) != 0 {
		t.Fatalf("S3 OCI indexes = %#v, %v; want none", indexes, err)
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

	time.Sleep(cacheTTL + 100*time.Millisecond)
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
	invalidKey := cacheA.Key(groupName, groupName+"/app", ociBlob, "invalid")
	if _, err := storeA.Get(context.Background(), invalidKey); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("invalid digest published cache index: %v", err)
	}
	upstream.mu.Lock()
	upstream.responses["invalid"] = integrationOCIResponse{status: http.StatusOK, content: []byte("valid")}
	upstream.mu.Unlock()
	time.Sleep(breakerTTL + 100*time.Millisecond)
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

func TestRawCachePublicationAndCollectionAcrossGatewayInstances(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	s3Endpoint := os.Getenv("TEST_RUSTFS_ENDPOINT")
	accessKey := os.Getenv("TEST_RUSTFS_ACCESS_KEY")
	secretKey := os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || s3Endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("PostgreSQL and S3 integration environment is required")
	}

	const bucket = "oci-cache-integration"
	storeA, err := NewRustFSOCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}
	if err := storeA.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	storeB, err := NewRustFSOCIObjectStore(s3Endpoint, accessKey, secretKey, bucket)
	if err != nil {
		t.Fatal(err)
	}

	unique := fmt.Sprintf("raw-publication-%d", time.Now().UnixNano())
	indexKey := NewDefaultRawCache(storeA, nil).Key(unique, "artifact", "proxy", "https://proxy.example")
	gatedStore := &rawPublicationGateStore{
		OCIObjectStore: storeA,
		indexKey:       indexKey,
		objectStored:   make(chan struct{}),
		allowIndex:     make(chan struct{}),
	}
	coordinatorA, err := NewPostgresCacheCoordinator(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinatorA.Close()
	coordinatorB, err := NewPostgresCacheCoordinator(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer coordinatorB.Close()
	cacheA := NewDefaultRawCache(gatedStore, nil).WithCoordinator(coordinatorA)
	cacheB := NewDefaultRawCache(storeB, nil).WithCoordinator(coordinatorB)
	content := RawContent{Body: []byte(unique), Repository: unique}

	stored := make(chan error, 1)
	go func() { stored <- cacheA.Store(context.Background(), indexKey, content) }()
	select {
	case <-gatedStore.objectStored:
	case <-time.After(5 * time.Second):
		t.Fatal("Raw object was not staged")
	}

	collected := make(chan error, 1)
	go func() { collected <- cacheB.CollectGarbage(context.Background()) }()
	select {
	case err := <-collected:
		t.Fatalf("collector completed before index publication: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(gatedStore.allowIndex)
	if err := <-stored; err != nil {
		t.Fatalf("store Raw content: %v", err)
	}
	if err := <-collected; err != nil {
		t.Fatalf("collect Raw cache: %v", err)
	}
	loaded, err := cacheB.Load(context.Background(), indexKey)
	if err != nil || string(loaded.Body) != string(content.Body) {
		t.Fatalf("final Raw cache content = %q, err = %v", loaded.Body, err)
	}
}
