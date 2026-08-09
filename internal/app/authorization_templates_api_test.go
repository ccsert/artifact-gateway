package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestAuthorizationTemplatesManagementHTTP(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "11111111-1111-1111-1111-111111111111", Name: "releases", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
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
	if got := request(http.MethodGet, "/api/v2/authorization-templates", "", "writer-secret", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin list = %d %s", got.Code, got.Body.String())
	}
	created := request(http.MethodPost, "/api/v2/authorization-templates", `{"name":"release-readers","grants":[{"principal":"user:alice","scopes":["repositories:read"],"resourcePrefix":"com.example"}]}`, "admin-secret", "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	var template struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(created.Body).Decode(&template); err != nil || template.ID == "" || template.Version != "1" {
		t.Fatalf("template = %#v err=%v", template, err)
	}
	apply := request(http.MethodPost, "/api/v2/authorization-templates/"+template.ID+"/apply", `{"repositoryId":"`+repo.ID+`"}`, "admin-secret", "1")
	if apply.Code != http.StatusOK || apply.Header().Get("ETag") != "2" || !strings.Contains(apply.Body.String(), "user:alice") {
		t.Fatalf("apply = %d etag=%q %s", apply.Code, apply.Header().Get("ETag"), apply.Body.String())
	}
	stale := request(http.MethodPost, "/api/v2/authorization-templates/"+template.ID+"/apply", `{"repositoryId":"`+repo.ID+`"}`, "admin-secret", "1")
	if stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale apply = %d %s", stale.Code, stale.Body.String())
	}
	bad := request(http.MethodPost, "/api/v2/authorization-templates", `{"name":"bad","grants":[{"principal":"user:alice","scopes":[]}]}`, "admin-secret", "")
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad template = %d %s", bad.Code, bad.Body.String())
	}
}
