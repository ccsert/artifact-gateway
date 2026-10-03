package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestRuntimeLogErrorsMatchDeclaredProblemContract(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := operationalog.NewBuffer(1)
	h := runtimeLogFixture(b, "node", "session")
	initial := runtimeLogPage(t, h, nil)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name, path, code string
		status           int
		handler          http.Handler
		ctx              context.Context
	}{
		{"disabled", "", "log_buffer_unavailable", 503, runtimeLogFixture(nil, "node", "session"), nil},
		{"identity", "", "log_source_identity_unavailable", 503, runtimeLogFixture(b, "node", ""), nil},
		{"remote", "?instanceId=other", "remote_log_query_unavailable", 503, h, nil},
		{"window", "?from=2026-01-01T00:00:00Z&to=2026-01-03T00:00:00Z", "invalid_time_window", 400, h, nil},
		{"limit", "?limit=101", "invalid_limit", 400, h, nil},
		{"cursor", "?afterCursor=bad", "invalid_cursor", 400, h, nil},
		{"filter", "?component=" + url.QueryEscape("http\x1b"), "invalid_filter", 400, h, nil},
		{"session", "?afterCursor=" + url.QueryEscape(*initial.AfterCursor), "log_cursor_scope_changed", 409, runtimeLogFixture(operationalog.NewBuffer(1), "node", "new-session"), nil},
		{"timeout", "", "log_query_timeout", 504, h, canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v2/runtime/logs"+tc.path, nil)
			if tc.ctx != nil {
				r = r.WithContext(tc.ctx)
			}
			authorize(r, "admin-secret")
			w := httptest.NewRecorder()
			tc.handler.ServeHTTP(w, r)
			assertRuntimeLogProblemContract(t, spec, w, tc.status, tc.code)
		})
	}
	store := repository.NewMemoryStore()
	_, err = store.CreateUser(context.Background(), repository.User{ID: "synthetic-reset-user", Name: "runtime-contract-reset", Role: string(RoleAdmin), MustChangePassword: true, SecretHash: "synthetic-password-hash"})
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticatorWithUsers(store)
	guarded := NewGatewayHandler(Dependencies{LogBuffer: b, Runtime: DiagnosticRuntime{InstanceID: "node", SessionID: "session"}}, store, TestAdapter{}, auth)
	assertRuntimeLogProblemContract(t, spec, requestRuntimeLogs(guarded, auth.IssueToken("user:runtime-contract-reset")), 403, "password_change_required")
}

func assertRuntimeLogProblemContract(t *testing.T, spec *openapi3.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != status || body["code"] != code || w.Header().Get("Cache-Control") != "no-store" || body["items"] != nil || body["afterCursor"] != nil {
		t.Fatalf("error contract: %d %s", w.Code, w.Body.String())
	}
	path := spec.Paths.Find("/runtime/logs")
	if path == nil || path.Get == nil {
		t.Fatal("runtime log GET operation is not declared")
	}
	response := path.Get.Responses.Status(status)
	if response == nil {
		t.Fatalf("HTTP %d response is not declared", status)
	}
	content := response.Value.Content["application/problem+json"]
	if content == nil || content.Schema == nil {
		t.Fatalf("HTTP %d problem schema is not declared", status)
	}
	if err := content.Schema.Value.VisitJSON(body); err != nil {
		t.Fatalf("HTTP %d %s violates its declared response schema: %v", status, code, err)
	}
}
