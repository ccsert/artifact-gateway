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
		{InstanceID: "api-01", Roles: []string{"api"}, StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Second)},
		{InstanceID: "worker-01", Roles: []string{"worker"}, WorkerFormats: []string{"oci"}, WorkerKinds: []string{"reclaim", "replication"}, StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Minute)},
		{InstanceID: "scheduler-01", Roles: []string{"scheduler"}, StartedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-3 * time.Minute)},
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
	if byID["api-01"].Status != adminopenapi.Online || byID["worker-01"].Status != adminopenapi.Stale || byID["scheduler-01"].Status != adminopenapi.Offline {
		t.Fatalf("runtime node statuses=%#v", byID)
	}
	worker := byID["worker-01"]
	if len(worker.WorkerFormats) != 1 || worker.WorkerFormats[0] != adminopenapi.FormatOci || len(worker.WorkerKinds) != 2 {
		t.Fatalf("worker capabilities=%#v", worker)
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
