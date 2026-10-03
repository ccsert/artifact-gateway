package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRuntimeLogQueryIsAdministratorOnlyLocalAndBounded(t *testing.T) {
	buffer := operationalog.NewBuffer(4)
	logger := operationalog.NewLogger(buffer, "gateway-01", "session-01")
	logger.Error("worker failed", "component", "worker", "operation", "job.run", "requestId", "job-1", "traceId", "trace-1", "apiToken", "private-token")
	logger.Info("worker resumed", "component", "worker", "operation", "job.run", "requestId", "job-1")
	dependencies := Dependencies{LogBuffer: buffer, Runtime: DiagnosticRuntime{InstanceID: "gateway-01", SessionID: "session-01"}}
	handler := NewGatewayHandler(dependencies, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	request := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			authorize(r, token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	for _, token := range []string{"", "reader-secret"} {
		if got := request("/api/v2/runtime/logs", token); got.Code != http.StatusUnauthorized || got.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("token %q status %d: %s", token, got.Code, got.Body.String())
		}
	}
	first := request("/api/v2/runtime/logs?requestId=job-1&limit=1", "admin-secret")
	if first.Code != http.StatusOK || first.Header().Get("Cache-Control") != "no-store" || strings.Contains(first.Body.String(), "private-token") {
		t.Fatalf("first status %d: %s", first.Code, first.Body.String())
	}
	var page adminopenapi.RuntimeLogPage
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Scope != "local" || page.InstanceId != "gateway-01" || page.SessionId != "session-01" || len(page.Items) != 1 || page.NextSequence == nil {
		t.Fatalf("first page: %#v", page)
	}
	second := request("/api/v2/runtime/logs?requestId=job-1&limit=1&beforeSequence=2", "admin-secret")
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "worker failed") || strings.Contains(second.Body.String(), "private-token") {
		t.Fatalf("second status %d: %s", second.Code, second.Body.String())
	}
	for _, test := range []struct {
		path string
		want int
	}{
		{"/api/v2/runtime/logs?instanceId=gateway-02", http.StatusServiceUnavailable},
		{"/api/v2/runtime/logs?limit=101", http.StatusBadRequest},
		{"/api/v2/runtime/logs?from=not-a-date", http.StatusBadRequest},
		{"/api/v2/runtime/logs?limit=not-a-number", http.StatusBadRequest},
		{"/api/v2/runtime/logs?from=2026-09-01T00:00:00Z&to=2026-09-03T00:00:00Z", http.StatusBadRequest},
	} {
		if got := request(test.path, "admin-secret"); got.Code != test.want || got.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s status %d, want %d: %s", test.path, got.Code, test.want, got.Body.String())
		}
	}
}

func TestRuntimeLogQueryReportsDisabledBuffer(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{Runtime: DiagnosticRuntime{InstanceID: "gateway-01"}}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/runtime/logs", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "log_buffer_unavailable") {
		t.Fatalf("disabled buffer status %d: %s", response.Code, response.Body.String())
	}
}
