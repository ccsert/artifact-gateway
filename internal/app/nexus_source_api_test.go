package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

func TestMigrationSourceManagementRequiresPlatformAdministrator(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := newTestGateway(t, GatewayOptions{Dependencies: Dependencies{}, Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	for _, methodPath := range []string{"GET /api/v2/migration-sources", "POST /api/v2/migration-sources", "GET /api/v2/migration-sources/00000000-0000-4000-8000-000000000001", "PUT /api/v2/migration-sources/00000000-0000-4000-8000-000000000001", "DELETE /api/v2/migration-sources/00000000-0000-4000-8000-000000000001", "POST /api/v2/migration-sources/00000000-0000-4000-8000-000000000001/preflight", "POST /api/v2/migration-sources/00000000-0000-4000-8000-000000000001/discovery", "POST /api/v2/migration-sources/00000000-0000-4000-8000-000000000001/mapping-preview"} {
		method, path, _ := strings.Cut(methodPath, " ")
		for _, token := range []string{"resolver-secret", "admin-secret"} {
			r := httptest.NewRequest(method, path, strings.NewReader(`{"credentials":{"password":"synthetic-secret-never-echo"}}`))
			authorize(r, token)
			r.Header.Set("If-Match", "1")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			want := 403
			if token == "admin-secret" {
				want = 503
			}
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "synthetic-secret") {
				t.Fatalf("%s token=%s status=%d body=%s cache=%s", methodPath, token, w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
			}
		}
	}
	audits, err := store.ListAudits(context.Background(), repository.AuditQuery{Limit: 100})
	if err != nil || len(audits) != 16 {
		t.Fatalf("audit count=%d err=%v", len(audits), err)
	}
	for _, audit := range audits {
		if !strings.HasPrefix(audit.Operation, "migration.source.") {
			t.Fatalf("unbounded audit operation: %#v", audit)
		}
	}
}
