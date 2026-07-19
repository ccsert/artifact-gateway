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
	if err := cache.Store(context.Background(), cache.key("team", "team/app", ociManifest, "latest"), CachedOCIContent{Body: content, Digest: digestOf(content)}); err != nil {
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
