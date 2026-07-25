package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestCacheMaintenanceReportsCapacityAndCleanupState(t *testing.T) {
	objectStore := NewMemoryOCIObjectStore()
	cache := NewOCICache(objectStore, time.Hour, time.Hour, time.Hour, nil)
	maintenance := NewCacheMaintenance(objectStore, cache)
	content := []byte("cached")
	if err := cache.Store(context.Background(), cache.Key("team", "team/app", ociManifest, "latest"), CachedOCIContent{Body: content, Digest: digestOf(content)}); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	status, err := maintenance.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ObjectCount != 1 || status.Bytes != int64(len(content)) || status.SuccessfulRuns != 1 || status.LastCompletedAt.IsZero() {
		t.Fatalf("status = %#v", status)
	}
}

func TestCacheCollectionRequiresAdministratorAndRunsCollector(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	maintenance := NewCacheMaintenance(store, NewDefaultOCICache(store, nil))
	handler := NewGatewayHandlerWithCacheMaintenance(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator(), NewDefaultOCICache(store, nil), nil, maintenance)

	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/api/v1/operations/cache/collect", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated status = %d", denied.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/operations/cache/collect", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("collection status = %d: %s", response.Code, response.Body.String())
	}
	if status := maintenance.snapshot(); status.SuccessfulRuns != 1 || status.FailedRuns != 0 {
		t.Fatalf("maintenance status = %#v", status)
	}
}

func TestCacheMaintenanceSchedulerStopsWithContext(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	maintenance := NewCacheMaintenance(store, NewDefaultOCICache(store, nil))
	ctx, cancel := context.WithCancel(context.Background())
	maintenance.Start(ctx, time.Millisecond)
	deadline := time.After(100 * time.Millisecond)
	for maintenance.snapshot().SuccessfulRuns == 0 {
		select {
		case <-deadline:
			t.Fatal("scheduled cleanup did not run")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	runs := maintenance.snapshot().SuccessfulRuns
	time.Sleep(5 * time.Millisecond)
	if got := maintenance.snapshot().SuccessfulRuns; got != runs {
		t.Fatalf("cleanup continued after cancellation: got %d, want %d", got, runs)
	}
}

func TestCacheOperationsRequiresAdminAndReturnsStatus(t *testing.T) {
	store := repository.NewMemoryStore()
	objectStore := NewMemoryOCIObjectStore()
	cache := NewDefaultOCICache(objectStore, nil)
	handler := NewGatewayHandlerWithCacheMaintenance(Dependencies{}, store, TestAdapter{}, testAuthenticator(), cache, nil, NewCacheMaintenance(objectStore, cache))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/operations/cache", nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("resolver status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/operations/cache", nil)
	authorize(request, "admin-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("admin status = %d body=%s", response.Code, response.Body.String())
	}
	var status CacheMaintenanceStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryOperationsReportsRepositoryMetrics(t *testing.T) {
	store := repository.NewMemoryStore()
	_, err := store.CreateGroup(context.Background(), repository.Group{Name: "team", Members: []repository.Member{{Name: "proxy", Type: repository.MemberProxy, Endpoint: "https://registry.example", Position: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	objectStore := NewMemoryOCIObjectStore()
	cache := NewOCICache(objectStore, time.Hour, time.Hour, time.Hour, []string{"registry.example"})
	client := &countingOCIClient{content: []byte(`{"schemaVersion":2}`), status: http.StatusOK}
	handler := NewGatewayHandlerWithCacheMaintenance(Dependencies{}, store, TestAdapter{}, testAuthenticator(), cache, nil, NewCacheMaintenance(objectStore, cache), client)

	for range 2 {
		request := httptest.NewRequest(http.MethodGet, "/v2/team/app/manifests/latest", nil)
		authorize(request, "resolver-secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("artifact status = %d body=%s", response.Code, response.Body.String())
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/operations/repositories?repository=team/app", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("admin status = %d body=%s", response.Code, response.Body.String())
	}
	var status RepositoryOperationsStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Metrics.Requests != 2 || status.Metrics.CacheHits == 0 || status.Metrics.CacheMisses == 0 || status.HitRate <= 0 || status.GatewayCache.ObjectCount != 1 {
		t.Fatalf("status = %#v", status)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/operations/repositories", nil)
	authorize(request, "admin-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing repository status = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/operations/repositories?repository=team/app", nil)
	authorize(request, "resolver-secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("resolver status = %d", response.Code)
	}
}
