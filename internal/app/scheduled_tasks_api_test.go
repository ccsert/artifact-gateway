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
	"github.com/google/uuid"
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

func TestScheduledTaskRepositoryTierFollowsTheTargetRepository(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	managed, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "task-managed", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "task-other", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	for repositoryID, principal := range map[string]string{managed.ID: "repo-admin", other.ID: "other-admin"} {
		if _, err := store.ReplaceRepositoryGrants(ctx, repositoryID, []repository.RepositoryGrant{{Principal: principal, Scopes: []string{"repositories:admin"}}}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	// Running a retention task needs the repository's retention policy enabled,
	// which is a business precondition rather than the gate under test.
	if _, err := store.ReplaceRepositoryRetentionPolicy(ctx, managed.ID, repository.RepositoryRetentionPolicy{Enabled: true, KeepDays: 36500, SnapshotKeepDays: 36500, MinimumVersions: 1, MaximumVersions: 10}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	call := func(token, method, path, body, version string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, token)
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	createTask := func(name, repositoryID string) adminopenapi.ScheduledTask {
		t.Helper()
		body := `{"name":"` + name + `","kind":"repository-retention","repositoryId":"` + repositoryID + `","intervalMinutes":60,"enabled":true}`
		response := call("admin-secret", http.MethodPost, "/api/v2/scheduled-tasks", body, "")
		if response.Code != http.StatusCreated {
			t.Fatalf("create %s=%d body=%s", name, response.Code, response.Body.String())
		}
		var task adminopenapi.ScheduledTask
		if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
			t.Fatal(err)
		}
		return task
	}
	managedTask := createTask("Managed retention", managed.ID)
	otherTask := createTask("Other retention", other.ID)
	global := call("admin-secret", http.MethodPost, "/api/v2/scheduled-tasks", `{"name":"Audit cleanup","kind":"audit-retention","intervalMinutes":1440,"enabled":true}`, "")
	if global.Code != http.StatusCreated {
		t.Fatalf("create global=%d body=%s", global.Code, global.Body.String())
	}
	var globalTask adminopenapi.ScheduledTask
	if err := json.Unmarshal(global.Body.Bytes(), &globalTask); err != nil {
		t.Fatal(err)
	}

	repoAdmin := authenticator.IssueToken("repo-admin")
	otherAdmin := authenticator.IssueToken("other-admin")

	// The catalogue shows each caller exactly the tasks it administers.
	listed := call(repoAdmin, http.MethodGet, "/api/v2/scheduled-tasks", "", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}
	var page []adminopenapi.ScheduledTask
	if err := json.Unmarshal(listed.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].Id != managedTask.Id {
		t.Fatalf("repository administrator sees %#v", page)
	}
	adminListed := call("admin-secret", http.MethodGet, "/api/v2/scheduled-tasks", "", "")
	var adminPage []adminopenapi.ScheduledTask
	if err := json.Unmarshal(adminListed.Body.Bytes(), &adminPage); err != nil {
		t.Fatal(err)
	}
	if len(adminPage) != 3 {
		t.Fatalf("platform administrator sees %d tasks", len(adminPage))
	}

	// The administered task is fully manageable; the other repository's task and
	// the platform-wide task are not.
	managedPath := "/api/v2/scheduled-tasks/" + managedTask.Id.String()
	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{name: "read", method: http.MethodGet, path: managedPath, want: http.StatusOK},
		{name: "runs", method: http.MethodGet, path: managedPath + "/runs?limit=10", want: http.StatusOK},
		{name: "run", method: http.MethodPost, path: managedPath + "/run", want: http.StatusAccepted},
		{name: "update", method: http.MethodPut, path: managedPath, body: `{"name":"Managed retention","kind":"repository-retention","repositoryId":"` + managed.ID + `","intervalMinutes":120,"enabled":true}`, want: http.StatusOK},
		{name: "other repository", method: http.MethodGet, path: "/api/v2/scheduled-tasks/" + otherTask.Id.String(), want: http.StatusForbidden},
		{name: "platform-wide", method: http.MethodGet, path: "/api/v2/scheduled-tasks/" + globalTask.Id.String(), want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := call(repoAdmin, tc.method, tc.path, tc.body, managedTask.Version)
			if response.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.want, response.Body.String())
			}
		})
	}

	// A task may not be moved out of the caller's repositories, and a task may
	// not be created for a repository the caller does not administer.
	move := call(repoAdmin, http.MethodPut, managedPath, `{"name":"Managed retention","kind":"repository-retention","repositoryId":"`+other.ID+`","intervalMinutes":120,"enabled":true}`, managedTask.Version)
	if move.Code != http.StatusForbidden {
		t.Fatalf("move=%d body=%s", move.Code, move.Body.String())
	}
	foreign := call(repoAdmin, http.MethodPost, "/api/v2/scheduled-tasks", `{"name":"Foreign retention","kind":"repository-retention","repositoryId":"`+other.ID+`","intervalMinutes":60,"enabled":true}`, "")
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("foreign create=%d body=%s", foreign.Code, foreign.Body.String())
	}
	if response := call(otherAdmin, http.MethodGet, managedPath, "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("other administrator read=%d body=%s", response.Code, response.Body.String())
	}

	// The administered task is deletable, and the deletion is audited.
	deleted := call(repoAdmin, http.MethodDelete, managedPath, "", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete=%d body=%s", deleted.Code, deleted.Body.String())
	}
	var deniedAudit, deleteAudit bool
	for _, audit := range store.Audits {
		if audit.Actor != "repo-admin" {
			continue
		}
		if audit.Outcome == repository.AuditAccessDenied && audit.AuthorizationReason == "scope_not_granted" {
			deniedAudit = true
		}
		if audit.Operation == "scheduled_task.delete" && audit.Status == http.StatusNoContent {
			deleteAudit = true
		}
	}
	if !deniedAudit || !deleteAudit {
		t.Fatalf("audits=%#v", store.Audits)
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
