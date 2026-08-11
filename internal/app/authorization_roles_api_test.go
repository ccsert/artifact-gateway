package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestAuthorizationRolesManagementHTTP(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(method, path, body, token, ifMatch string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			authorize(r, token)
		}
		if ifMatch != "" {
			r.Header.Set("If-Match", ifMatch)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := request(http.MethodGet, "/api/v2/authorization-roles", "", "writer-secret", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin list = %d %s", got.Code, got.Body.String())
	}
	created := request(http.MethodPost, "/api/v2/authorization-roles", `{"name":"release-reviewer","description":"read and inspect","scopes":["repositories:read","repositories:intelligence"]}`, "admin-secret", "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var role struct {
		ID      string   `json:"id"`
		Scopes  []string `json:"scopes"`
		Version string   `json:"version"`
	}
	if err := json.NewDecoder(created.Body).Decode(&role); err != nil || role.ID == "" || role.Version != "1" || len(role.Scopes) != 2 {
		t.Fatalf("role = %#v err=%v", role, err)
	}
	listed := request(http.MethodGet, "/api/v2/authorization-roles", "", "admin-secret", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "release-reviewer") {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
	updated := request(http.MethodPut, "/api/v2/authorization-roles/"+role.ID, `{"name":"release-owner","scopes":["repositories:admin"]}`, "admin-secret", "1")
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != "2" || !strings.Contains(updated.Body.String(), "release-owner") {
		t.Fatalf("update = %d etag=%q %s", updated.Code, updated.Header().Get("ETag"), updated.Body.String())
	}
	stale := request(http.MethodPut, "/api/v2/authorization-roles/"+role.ID, `{"name":"stale","scopes":["repositories:read"]}`, "admin-secret", "1")
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale update = %d %s", stale.Code, stale.Body.String())
	}
	bad := request(http.MethodPost, "/api/v2/authorization-roles", `{"name":"bad","scopes":[]}`, "admin-secret", "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad role = %d %s", bad.Code, bad.Body.String())
	}
	deleted := request(http.MethodDelete, "/api/v2/authorization-roles/"+role.ID, "", "admin-secret", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
}
