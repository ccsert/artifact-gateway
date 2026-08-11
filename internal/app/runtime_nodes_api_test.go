package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRuntimeNodesAPIListsHeartbeatStatusAndWorkerCapabilities(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	now := time.Now().UTC()
	for _, node := range []repository.RuntimeNode{
		{InstanceID: "api-01", SessionID: "session-api", Roles: []string{"api"}, StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Second)},
		{InstanceID: "worker-01", SessionID: "session-worker", Roles: []string{"worker"}, WorkerFormats: []string{"oci"}, WorkerKinds: []string{"reclaim", "replication"}, StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Minute)},
		{InstanceID: "scheduler-01", SessionID: "session-scheduler", Roles: []string{"scheduler"}, StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-3 * time.Minute)},
	} {
		if err := store.UpsertRuntimeNodeHeartbeat(ctx, node); err != nil {
			t.Fatal(err)
		}
	}

	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/runtime/nodes", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("runtime nodes=%d body=%s", response.Code, response.Body.String())
	}
	var body adminopenapi.RuntimeNodeList
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 3 {
		t.Fatalf("runtime nodes=%#v", body.Items)
	}
	byID := make(map[string]adminopenapi.RuntimeNode, len(body.Items))
	for _, node := range body.Items {
		byID[node.InstanceId] = node
	}
	if byID["api-01"].Status != adminopenapi.RuntimeNodeStatusOnline || byID["worker-01"].Status != adminopenapi.RuntimeNodeStatusStale || byID["scheduler-01"].Status != adminopenapi.RuntimeNodeStatusOffline {
		t.Fatalf("runtime node statuses=%#v", byID)
	}
	if body.Health.Status != adminopenapi.RuntimeNodeHealthStatusDegraded || body.Health.Online != 1 || body.Health.Stale != 1 || body.Health.Offline != 1 {
		t.Fatalf("runtime node health=%#v", body.Health)
	}
	worker := byID["worker-01"]
	if len(worker.WorkerFormats) != 1 || worker.WorkerFormats[0] != adminopenapi.FormatOci || len(worker.WorkerKinds) != 2 {
		t.Fatalf("worker capabilities=%#v", worker)
	}
}

func TestRuntimeNodeHealthReportsDuplicateSessionsAndMissingRoles(t *testing.T) {
	health := runtimeNodeHealth([]adminopenapi.RuntimeNode{
		{InstanceId: "api-01", SessionId: "api-session", Roles: []string{"api"}, Status: adminopenapi.RuntimeNodeStatusOnline},
		{InstanceId: "worker-01", SessionId: "worker-session-1", Roles: []string{"worker"}, Status: adminopenapi.RuntimeNodeStatusOnline},
		{InstanceId: "worker-01", SessionId: "worker-session-2", Roles: []string{"worker"}, Status: adminopenapi.RuntimeNodeStatusOnline},
	})
	if health.Status != adminopenapi.RuntimeNodeHealthStatusDegraded {
		t.Fatalf("health status=%q", health.Status)
	}
	if len(health.Issues) != 2 {
		t.Fatalf("health issues=%#v", health.Issues)
	}
	for _, issue := range health.Issues {
		if issue.Code == "duplicate_instance_id" {
			return
		}
	}
	t.Fatalf("duplicate session issue missing: %#v", health.Issues)
}

func TestRuntimeNodeStatusMarksThirtySecondsAsStale(t *testing.T) {
	now := time.Date(2026, 8, 8, 8, 0, 0, 0, time.UTC)
	if got := runtimeNodeStatus(now, now.Add(-30*time.Second)); got != adminopenapi.RuntimeNodeStatusStale {
		t.Fatalf("status at stale boundary=%q", got)
	}
	if got := runtimeNodeStatus(now, now.Add(-29*time.Second)); got != adminopenapi.RuntimeNodeStatusOnline {
		t.Fatalf("status before stale boundary=%q", got)
	}
}

func TestRuntimeNodesAPIRequiresAdministrator(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/runtime/nodes", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("runtime nodes without admin=%d", response.Code)
	}
}
