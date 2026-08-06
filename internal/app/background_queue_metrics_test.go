package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestBackgroundOperationQueueMetricsExposeDurableStateWithBoundedLabels(t *testing.T) {
	now := time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)
	metrics := &Metrics{now: func() time.Time { return now }}
	metrics.ReplaceBackgroundOperationQueueStats([]repository.BackgroundOperationQueueStat{
		{Kind: repository.BackgroundOperationPromotion, Format: repository.FormatRaw, State: repository.LifecycleJobPending, Count: 2, OldestCreatedAt: now.Add(-90 * time.Second)},
		{Kind: repository.BackgroundOperationPromotion, Format: repository.FormatRaw, State: repository.LifecycleJobRetrying, Count: 1, OldestCreatedAt: now.Add(-30 * time.Second)},
		{Kind: "unknown", Format: repository.FormatRaw, State: repository.LifecycleJobPending, Count: 99, OldestCreatedAt: now.Add(-time.Hour)},
	})

	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := response.Body.String()
	for _, want := range []string{
		`artifact_gateway_background_jobs{kind="promotion",format="raw",state="pending"} 2`,
		`artifact_gateway_background_jobs{kind="promotion",format="raw",state="retrying"} 1`,
		`artifact_gateway_background_queue_oldest_actionable_age_seconds{kind="promotion",format="raw"} 90`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "unknown") {
		t.Fatalf("unbounded queue metric label in:\n%s", body)
	}
}

func TestBackgroundOperationQueueObserverRefreshesMetricsFromStore(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	if _, _, err := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: "promotion", RepositoryID: "target", Kind: repository.LifecycleJobPromotion, IdempotencyKey: "promote", Payload: []byte(`{"format":"oci"}`)}); err != nil {
		t.Fatal(err)
	}
	metrics := &Metrics{}
	observer := BackgroundOperationQueueObserver{Store: store, Metrics: metrics}
	if err := observer.Run(ctx); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if body := response.Body.String(); !strings.Contains(body, `artifact_gateway_background_jobs{kind="promotion",format="oci",state="pending"} 1`) {
		t.Fatalf("metrics did not refresh from durable queue:\n%s", body)
	}
}

func TestBackgroundOperationQueueObserverStartsWithImmediateRefresh(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := repository.NewMemoryStore()
	if _, _, err := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: "reclaim", RepositoryID: "target", Kind: repository.LifecycleJobReclaim, IdempotencyKey: "reclaim", Payload: []byte(`{"format":"conan"}`)}); err != nil {
		t.Fatal(err)
	}
	metrics := &Metrics{}
	BackgroundOperationQueueObserver{Store: store, Metrics: metrics}.Start(ctx, time.Hour)

	deadline := time.Now().Add(time.Second)
	for {
		response := httptest.NewRecorder()
		metrics.Handler(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if strings.Contains(response.Body.String(), `artifact_gateway_background_jobs{kind="lifecycle",format="conan",state="pending"} 1`) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("observer did not refresh queue metrics at startup")
		}
		time.Sleep(time.Millisecond)
	}
}
