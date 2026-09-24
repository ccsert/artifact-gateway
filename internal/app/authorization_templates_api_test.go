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
	audits := map[string]repository.AuditRecord{}
	for _, record := range store.Audits {
		audits[record.Operation] = record
	}
	createdAudit, ok := audits["authorization_template.create"]
	if !ok || createdAudit.Actor != "alice" || createdAudit.Resource != "authorization-templates/"+template.ID ||
		createdAudit.Status != http.StatusCreated || createdAudit.Format != "management" {
		t.Fatalf("create audit = %#v", createdAudit)
	}
	appliedAudit, ok := audits["repository.grants.apply_template"]
	if !ok || appliedAudit.Actor != "alice" || appliedAudit.Repository != repo.Name || appliedAudit.GroupName != repo.Name ||
		appliedAudit.Resource != "repositories/"+repo.ID+"/grants" || appliedAudit.Status != http.StatusOK {
		t.Fatalf("apply audit = %#v", appliedAudit)
	}
	if len(store.Audits) != 2 {
		t.Fatalf("rejected requests must not be audited as mutations: %#v", store.Audits)
	}
	updated := request(http.MethodPut, "/api/v2/authorization-templates/"+template.ID, `{"name":"release-readers","description":"updated","grants":[{"principal":"user:bob","scopes":["repositories:read"]}]}`, "admin-secret", "1")
	if updated.Code != http.StatusOK || updated.Header().Get("ETag") != "2" {
		t.Fatalf("update = %d etag=%q %s", updated.Code, updated.Header().Get("ETag"), updated.Body.String())
	}
	deleted := request(http.MethodDelete, "/api/v2/authorization-templates/"+template.ID, "", "admin-secret", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
	if record := store.Audits[len(store.Audits)-1]; record.Operation != "authorization_template.delete" || record.Status != http.StatusNoContent || record.Resource != "authorization-templates/"+template.ID {
		t.Fatalf("delete audit = %#v", record)
	}
	if record := store.Audits[len(store.Audits)-2]; record.Operation != "authorization_template.update" || record.Status != http.StatusOK || record.Resource != "authorization-templates/"+template.ID {
		t.Fatalf("update audit = %#v", record)
	}
}
