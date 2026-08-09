package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestScheduledTaskManagementLifecycleAndDispatchHistory(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body, version string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, "admin-secret")
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	created := request(http.MethodPost, "/api/v2/scheduled-tasks", `{"name":"Nightly audit cleanup","description":"Keep the audit table bounded","kind":"audit-retention","intervalMinutes":1440,"enabled":true}`, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d body=%s", created.Code, created.Body.String())
	}
	var task adminopenapi.ScheduledTask
	if err := json.Unmarshal(created.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	if task.Version != "1" || task.Name != "Nightly audit cleanup" || task.NextRunAt.IsZero() {
		t.Fatalf("created task = %#v", task)
	}

	listed := request(http.MethodGet, "/api/v2/scheduled-tasks", "", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), task.Id.String()) {
		t.Fatalf("list = %d body=%s", listed.Code, listed.Body.String())
	}

	stale := request(http.MethodPut, "/api/v2/scheduled-tasks/"+task.Id.String(), `{"name":"Audit cleanup","kind":"audit-retention","intervalMinutes":60,"enabled":false}`, "0")
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale = %d body=%s", stale.Code, stale.Body.String())
	}
	updated := request(http.MethodPut, "/api/v2/scheduled-tasks/"+task.Id.String(), `{"name":"Audit cleanup","kind":"audit-retention","intervalMinutes":60,"enabled":false}`, task.Version)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"version":"2"`) || !strings.Contains(updated.Body.String(), `"enabled":false`) {
		t.Fatalf("update = %d body=%s", updated.Code, updated.Body.String())
	}

	failedRun := request(http.MethodPost, "/api/v2/scheduled-tasks/"+task.Id.String()+"/run", "", "")
	if failedRun.Code != http.StatusConflict || !strings.Contains(failedRun.Body.String(), "disabled") {
		t.Fatalf("failed run = %d body=%s", failedRun.Code, failedRun.Body.String())
	}
	policy, err := store.GetAuditRetentionPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	policy.Enabled, policy.KeepDays = true, 30
	if _, err = store.ReplaceAuditRetentionPolicy(context.Background(), policy, policy.Version); err != nil {
		t.Fatal(err)
	}
	submitted := request(http.MethodPost, "/api/v2/scheduled-tasks/"+task.Id.String()+"/run", "", "")
	if submitted.Code != http.StatusAccepted || !strings.Contains(submitted.Body.String(), `"state":"submitted"`) || !strings.Contains(submitted.Body.String(), `"targetKind":"audit-cleanup"`) {
		t.Fatalf("submitted = %d body=%s", submitted.Code, submitted.Body.String())
	}

	runs := request(http.MethodGet, "/api/v2/scheduled-tasks/"+task.Id.String()+"/runs?limit=10", "", "")
	if runs.Code != http.StatusOK || !strings.Contains(runs.Body.String(), `"state":"failed"`) || !strings.Contains(runs.Body.String(), `"state":"submitted"`) {
		t.Fatalf("runs = %d body=%s", runs.Code, runs.Body.String())
	}
	deleted := request(http.MethodDelete, "/api/v2/scheduled-tasks/"+task.Id.String(), "", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete = %d body=%s", deleted.Code, deleted.Body.String())
	}
	notFound := request(http.MethodGet, "/api/v2/scheduled-tasks/"+task.Id.String(), "", "")
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("get deleted = %d body=%s", notFound.Code, notFound.Body.String())
	}
}

func TestScheduledTaskRejectsInvalidTargetAndNonAdmin(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	reader := httptest.NewRequest(http.MethodPost, "/api/v2/scheduled-tasks", strings.NewReader(`{"name":"audit","kind":"audit-retention","intervalMinutes":60,"enabled":true}`))
	authorize(reader, "reader-secret")
	readerResponse := httptest.NewRecorder()
	handler.ServeHTTP(readerResponse, reader)
	if readerResponse.Code != http.StatusUnauthorized {
		t.Fatalf("reader = %d body=%s", readerResponse.Code, readerResponse.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPost, "/api/v2/scheduled-tasks", strings.NewReader(`{"name":"retention","kind":"repository-retention","intervalMinutes":5,"enabled":true}`))
	authorize(invalid, "admin-secret")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid = %d body=%s", invalidResponse.Code, invalidResponse.Body.String())
	}
}
