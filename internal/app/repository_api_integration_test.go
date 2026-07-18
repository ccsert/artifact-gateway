//go:build integration

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestPostgresHTTPIntegration(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	group := `{"name":"engineering","members":[{"name":"proxy","type":"proxy","endpoint":"test://available","position":1},{"name":"hosted","type":"hosted","endpoint":"test://unavailable","position":0}]}`
	created := integrationRequest(handler, http.MethodPost, "/api/v1/oci/groups", group, "admin-secret")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	got := integrationRequest(handler, http.MethodGet, "/api/v1/oci/groups/engineering", "", "admin-secret")
	if got.Code != http.StatusOK {
		t.Fatalf("get = %d %s", got.Code, got.Body.String())
	}
	var stored repository.Group
	if err := json.NewDecoder(got.Body).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Members) != 2 || stored.Members[0].Name != "hosted" || stored.Members[1].Type != repository.MemberProxy {
		t.Fatalf("stored group = %#v", stored)
	}
	resolved := integrationRequest(handler, http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", "", "resolver-secret")
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"name":"proxy"`) {
		t.Fatalf("resolve = %d %s", resolved.Code, resolved.Body.String())
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var actor, outcome, member string
	if err := db.QueryRowContext(context.Background(), `SELECT actor, outcome, member_name FROM resolver_audit_log WHERE group_name=$1`, "engineering").Scan(&actor, &outcome, &member); err != nil {
		t.Fatal(err)
	}
	if actor != "build-agent" || outcome != string(repository.AuditResolved) || member != "proxy" {
		t.Fatalf("audit = actor=%q outcome=%q member=%q", actor, outcome, member)
	}
	disabled := integrationRequest(handler, http.MethodPost, "/api/v1/oci/groups/engineering/disable", "", "admin-secret")
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable = %d %s", disabled.Code, disabled.Body.String())
	}
	blocked := integrationRequest(handler, http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", "", "resolver-secret")
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), `group_disabled`) {
		t.Fatalf("disabled resolve = %d %s", blocked.Code, blocked.Body.String())
	}
}

func integrationRequest(handler http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	authorize(request, token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
