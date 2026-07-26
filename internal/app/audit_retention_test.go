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

func TestAuditRetentionHTTPAndWorker(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	store.Audits = []repository.AuditRecord{
		{Actor: "old", OccurredAt: time.Now().UTC().AddDate(0, 0, -10)},
		{Actor: "recent", OccurredAt: time.Now().UTC()},
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, "admin-secret")
		if method == http.MethodPut {
			r.Header.Set("If-Match", "1")
		}
		if method == http.MethodPost {
			r.Header.Set("Idempotency-Key", "audit-cleanup")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, "/api/v2/audit-retention-policy", ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"enabled":false`) {
		t.Fatalf("default policy = %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPost, "/api/v2/audit-retention:execute", ""); got.Code != http.StatusConflict {
		t.Fatalf("disabled execute = %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPut, "/api/v2/audit-retention-policy", `{"version":"ignored","enabled":true,"keepDays":1}`); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"version":"2"`) {
		t.Fatalf("replace = %d %s", got.Code, got.Body.String())
	}
	first := request(http.MethodPost, "/api/v2/audit-retention:execute", "")
	replay := request(http.MethodPost, "/api/v2/audit-retention:execute", "")
	if first.Code != http.StatusAccepted || replay.Code != http.StatusAccepted || first.Body.String() != replay.Body.String() {
		t.Fatalf("idempotent execute first=%d %s replay=%d %s", first.Code, first.Body.String(), replay.Code, replay.Body.String())
	}
	if err := (AuditRetentionWorker{Store: store}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if len(store.Audits) != 1 || store.Audits[0].Actor != "recent" {
		t.Fatalf("audits after cleanup = %#v", store.Audits)
	}
	if got := request(http.MethodGet, "/api/v2/audit-retention/jobs", ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"state":"completed"`) || !strings.Contains(got.Body.String(), `"deleted":1`) {
		t.Fatalf("jobs = %d %s", got.Code, got.Body.String())
	}
}

func TestAuditRetentionWorkerRetriesFailedJob(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	p, err := store.ReplaceAuditRetentionPolicy(ctx, repository.AuditRetentionPolicy{Enabled: true, KeepDays: 1}, "1")
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.EnqueueAuditCleanupJob(ctx, repository.AuditCleanupJob{ID: "job", IdempotencyKey: "retry", PolicyVersion: p.Version, CutoffAt: time.Now().UTC(), BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimAuditCleanupJobs(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	if err := store.FailAuditCleanupJob(ctx, job.ID, "temporary failure"); err != nil {
		t.Fatal(err)
	}
	if err := (AuditRetentionWorker{Store: store}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListAuditCleanupJobs(ctx, 1)
	if err != nil || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
}
