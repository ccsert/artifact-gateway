package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

// These ports intentionally do not expose artifact, identity, or lifecycle
// methods: the domain is exercised without constructing a Gateway.
type repositoryConfigurationPort struct {
	repository.HostedRepositoryStore
}
type repositoryGroupPort struct{ repository.HostedGroupStore }

type managementAuditFixture struct{ records []repository.AuditRecord }

func (s *managementAuditFixture) RecordAudit(_ context.Context, record repository.AuditRecord) error {
	s.records = append(s.records, record)
	return nil
}

func TestRepositoryManagementDomainIndependentHTTP(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repos, groups := repositoryConfigurationPort{store}, repositoryGroupPort{store}
	audit := &managementAuditFixture{}
	auth := testAuthenticator()
	auth.LegacyReadPermissive = false
	auth.RepositoryReaders = map[string][]string{}
	scope := newManagementScope(repos, nil, store, audit, auth, nil)
	domain := newRepositoryManagementHandler(repositoryManagementOptions{Scope: scope, Repositories: repos, Groups: groups, Capacities: store})
	// The other domain adapter is empty; these routes never construct or consume it.
	server := adminopenapi.HandlerWithOptions(managementServer{repositoryManagementHandler: domain}, adminopenapi.StdHTTPServerOptions{BaseURL: "/api/v2", BaseRouter: openAPIServeMux{mux: http.NewServeMux()}, ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
	}})
	call := func(token, method, path, body, key, version string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			authorize(request, token)
		}
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		if version != "" {
			request.Header.Set("If-Match", version)
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}
	body := `{"name":"domain-raw","format":"raw"}`
	created := call("admin-secret", http.MethodPost, "/api/v2/repositories", body, "domain-create", "")
	var repo repository.HostedRepository
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &repo) != nil || repo.Name != "domain-raw" {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	replay := call("admin-secret", http.MethodPost, "/api/v2/repositories", body, "domain-create", "")
	var repeated repository.HostedRepository
	if replay.Code != http.StatusCreated || json.Unmarshal(replay.Body.Bytes(), &repeated) != nil || repeated.ID != repo.ID {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	if response := call("admin-secret", http.MethodPost, "/api/v2/repositories", `{"name":"other-raw","format":"raw"}`, "domain-create", ""); response.Code != http.StatusConflict {
		t.Fatalf("conflict=%d %s", response.Code, response.Body.String())
	}
	path := "/api/v2/repositories/" + repo.ID
	if response := call("", http.MethodGet, path, "", "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous=%d", response.Code)
	}
	reader := auth.IssueToken("domain-reader")
	if response := call(reader, http.MethodGet, path, "", "", ""); response.Code != http.StatusForbidden {
		t.Fatalf("denial=%d %s", response.Code, response.Body.String())
	}
	if len(audit.records) != 1 || audit.records[0].Actor != "domain-reader" || audit.records[0].Resource != "repositories/"+repo.ID || audit.records[0].Status != http.StatusForbidden {
		t.Fatalf("denial audit=%+v", audit.records)
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "domain-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if response := call(reader, http.MethodGet, path, "", "", ""); response.Code != http.StatusOK {
		t.Fatalf("read=%d %s", response.Code, response.Body.String())
	}
	if response := call(reader, http.MethodGet, "/api/v2/repositories?pageSize=1", "", "", ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "domain-raw") {
		t.Fatalf("catalog=%d %s", response.Code, response.Body.String())
	}
	if response := call(reader, http.MethodGet, "/api/v2/repositories?pageSize=invalid", "", "", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("binding=%d", response.Code)
	}
	groupBody := `{"name":"domain-group","format":"raw","members":[{"repositoryId":"` + repo.ID + `","position":0}]}`
	groupCreated := call("admin-secret", http.MethodPost, "/api/v2/groups", groupBody, "domain-group", "")
	var group adminopenapi.Group
	if groupCreated.Code != http.StatusCreated || json.Unmarshal(groupCreated.Body.Bytes(), &group) != nil || group.Name != "domain-group" {
		t.Fatalf("group=%d %s", groupCreated.Code, groupCreated.Body.String())
	}
	groupPath := "/api/v2/groups/" + group.Id.String()
	if response := call(reader, http.MethodGet, groupPath, "", "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("group existence=%d", response.Code)
	}
	if response := call("admin-secret", http.MethodGet, groupPath+"/resolution", "", "", ""); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), repo.ID) {
		t.Fatalf("resolution=%d %s", response.Code, response.Body.String())
	}
	if response := call("admin-secret", http.MethodPut, groupPath+"/members", `[{"repositoryId":"`+repo.ID+`","position":0}]`, "", "stale"); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("version=%d %s", response.Code, response.Body.String())
	}
	if response := call("admin-secret", http.MethodGet, groupPath+"/capacity", "", "", ""); response.Code != http.StatusOK {
		t.Fatalf("capacity=%d %s", response.Code, response.Body.String())
	}
}
