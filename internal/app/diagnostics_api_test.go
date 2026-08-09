package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestDiagnosticsRequiresAdministratorAndRedactsDependencyErrors(t *testing.T) {
	store := repository.NewMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	if err := store.UpsertRuntimeNodeHeartbeat(ctx, repository.RuntimeNode{
		InstanceID: "gateway-01", SessionID: "session-01", Roles: []string{"standalone"},
		WorkerFormats: []string{"maven"}, WorkerKinds: []string{"retention"},
		StartedAt: now, LastSeenAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
		ID: "job-1", RepositoryID: "repository-1", Kind: repository.LifecycleJobPromotion,
		IdempotencyKey: "diagnostics-job", Payload: json.RawMessage(`{"format":"maven"}`),
	}); err != nil {
		t.Fatal(err)
	}
	dependencies := Dependencies{
		checkers: []Checker{checkerFunc(func(context.Context) error {
			return errors.New("postgres://gateway:top-secret@database/gateway")
		})},
		BuildVersion: "v1.2.3", BuildRevision: "abc123", BuildGoVersion: "go1.test",
		Runtime: DiagnosticRuntime{
			InstanceID: "gateway-01", Roles: []string{"standalone"},
			WorkerFormats: []repository.Format{repository.FormatMaven}, WorkerKinds: []string{"retention"},
		},
	}
	handler := NewGatewayHandler(dependencies, store, TestAdapter{}, testAuthenticator())

	reader := httptest.NewRequest(http.MethodGet, "/api/v2/diagnostics", nil)
	authorize(reader, "reader-secret")
	readerResponse := httptest.NewRecorder()
	handler.ServeHTTP(readerResponse, reader)
	if readerResponse.Code != http.StatusUnauthorized {
		t.Fatalf("reader status = %d body=%s", readerResponse.Code, readerResponse.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v2/diagnostics", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "top-secret") || strings.Contains(response.Body.String(), "postgres://") {
		t.Fatalf("diagnostics leaked dependency error: %s", response.Body.String())
	}
	var diagnostics adminopenapi.Diagnostics
	if err := json.Unmarshal(response.Body.Bytes(), &diagnostics); err != nil {
		t.Fatal(err)
	}
	if diagnostics.Build.Version != "v1.2.3" || diagnostics.Build.Revision != "abc123" || diagnostics.Runtime.InstanceId != "gateway-01" {
		t.Fatalf("identity = %#v %#v", diagnostics.Build, diagnostics.Runtime)
	}
	if len(diagnostics.Dependencies) != 1 || diagnostics.Dependencies[0].Status != adminopenapi.Unreachable || diagnostics.Dependencies[0].Detail == nil || *diagnostics.Dependencies[0].Detail != "health check failed" {
		t.Fatalf("dependencies = %#v", diagnostics.Dependencies)
	}
	if len(diagnostics.Queues) != 1 || diagnostics.Queues[0].Kind != adminopenapi.DiagnosticQueueStatKindPromotion || diagnostics.Queues[0].Format != adminopenapi.Format(repository.FormatMaven) || diagnostics.Queues[0].Count != 1 {
		t.Fatalf("queues = %#v", diagnostics.Queues)
	}
	if diagnostics.Nodes.Status != adminopenapi.Healthy || diagnostics.Nodes.Online != 1 {
		t.Fatalf("nodes = %#v", diagnostics.Nodes)
	}
}

func TestDiagnosticsReportsMissingDependencyConfiguration(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/diagnostics", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"not_configured"`) {
		t.Fatalf("response = %d body=%s", response.Code, response.Body.String())
	}
}
