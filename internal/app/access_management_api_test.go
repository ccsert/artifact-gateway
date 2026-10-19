package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type accessGrantPort struct {
	repository.RepositoryGrantStore
}
type accessRolePort struct {
	repository.AuthorizationRoleStore
}
type accessTemplatePort struct {
	repository.AuthorizationTemplateStore
}

func TestAccessManagementDomainIndependentHTTP(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "domain-access", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticator()
	auth.LegacyReadPermissive = false
	auth.RepositoryReaders = map[string][]string{}
	audit := &managementAuditFixture{}
	grants := accessGrantPort{store}
	scope := newManagementScope(artifactCatalogPort{store}, artifactAnonymousPolicyPort{store}, grants, audit, auth, nil)
	domain := newAccessManagementHandler(accessManagementOptions{Scope: scope, Grants: grants, Roles: accessRolePort{store}, Templates: accessTemplatePort{store}})
	server := adminopenapi.HandlerWithOptions(managementServer{accessManagementHandler: domain}, adminopenapi.StdHTTPServerOptions{BaseURL: "/api/v2", BaseRouter: openAPIServeMux{mux: http.NewServeMux()}})
	call := func(token, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			authorize(r, token)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	path := "/api/v2/repositories/" + repo.ID
	reader := auth.IssueToken("user:domain-reader")
	grantBody := `{"principal":"user:domain-reader","scopes":["repositories:read"]}`
	response := call("admin-secret", http.MethodPost, path+"/grants", grantBody)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != "2" {
		t.Fatalf("grant=%d %s", response.Code, response.Body.String())
	}
	response = call(reader, http.MethodGet, path+"/grants", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("read does not grant admin=%d %s", response.Code, response.Body.String())
	}
	response = call(reader, http.MethodGet, path+"/effective-access", "")
	var access adminopenapi.RepositoryEffectiveAccess
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &access) != nil || !access.Permissions.Read.Allowed || access.Permissions.Admin.Allowed || access.Actor != "user:domain-reader" {
		t.Fatalf("effective=%d %s", response.Code, response.Body.String())
	}
	response = call(auth.IssueToken("user:outsider"), http.MethodGet, path+"/effective-access", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("existence is hidden=%d %s", response.Code, response.Body.String())
	}
	response = call("admin-secret", http.MethodGet, "/api/v2/repository-grants", "")
	if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "not_supported") {
		t.Fatalf("aggregation requires its explicit query port=%d %s", response.Code, response.Body.String())
	}
	response = call("admin-secret", http.MethodDelete, path+"/grants?principal="+url.QueryEscape("user:domain-reader"), "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("revoke=%d %s", response.Code, response.Body.String())
	}
	response = call(reader, http.MethodGet, path+"/effective-access", "")
	if response.Code != http.StatusNotFound {
		t.Fatalf("revoked access=%d %s", response.Code, response.Body.String())
	}
	operations := map[string]int{}
	for _, record := range audit.records {
		operations[record.Operation]++
		if record.Operation == "repository.grants.upsert" || record.Operation == "repository.grants.delete" {
			if record.Actor != "alice" || record.Resource != "repositories/"+repo.ID+"/grants" {
				t.Fatalf("mutation audit=%+v", record)
			}
		}
	}
	if operations["repository.grants.upsert"] != 1 || operations["repository.grants.delete"] != 1 || operations[string(RepositoryAdmin)] != 1 {
		t.Fatalf("audited operations=%v", operations)
	}
}
