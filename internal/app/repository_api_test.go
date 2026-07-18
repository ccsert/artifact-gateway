package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type selectiveAdapter struct{ available map[string]bool }

func (a selectiveAdapter) Available(_ context.Context, member repository.Member, _ string) bool {
	return a.available[member.Name]
}

type failingAuditStore struct{ *repository.MemoryStore }

func (f failingAuditStore) RecordAudit(context.Context, repository.AuditRecord) error {
	return errors.New("audit unavailable")
}

func testAuthenticator() Authenticator {
	return Authenticator{AdminToken: "admin-secret", ResolverToken: "resolver-secret", AdminActor: "alice", ResolverActor: "build-agent"}
}

func authorize(request *http.Request, token string) {
	request.Header.Set("Authorization", "Bearer "+token)
}

func TestGroupManagementAndResolverVerticalSlice(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, selectiveAdapter{available: map[string]bool{"proxy": true}}, testAuthenticator())
	group := `{"name":"engineering","members":[{"name":"hosted","type":"hosted","endpoint":"http://gitea","position":0},{"name":"proxy","type":"proxy","endpoint":"https://registry.example","position":1}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", strings.NewReader(group))
	authorize(request, "admin-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", created.Code, created.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/engineering", nil)
	authorize(request, "admin-secret")
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, request)
	if got.Code != http.StatusOK {
		t.Fatalf("get status = %d", got.Code)
	}
	var stored repository.Group
	if err := json.NewDecoder(got.Body).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Enabled || len(stored.Members) != 2 || stored.Members[0].Name != "hosted" || stored.Members[1].Type != repository.MemberProxy {
		t.Fatalf("stored group = %#v", stored)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", nil)
	authorize(request, "resolver-secret")
	resolved := httptest.NewRecorder()
	handler.ServeHTTP(resolved, request)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"name":"proxy"`) {
		t.Fatalf("resolve = %d %s", resolved.Code, resolved.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != repository.AuditResolved || store.Audits[0].MemberName != "proxy" {
		t.Fatalf("audits = %#v", store.Audits)
	}
	if store.Audits[0].Actor != "build-agent" {
		t.Fatalf("audit actor = %q", store.Audits[0].Actor)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups/engineering/disable", nil)
	authorize(request, "admin-secret")
	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, request)
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable status = %d", disabled.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", nil)
	authorize(request, "resolver-secret")
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, request)
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), `group_disabled`) {
		t.Fatalf("disabled resolve = %d %s", blocked.Code, blocked.Body.String())
	}
}

func TestAPIRejectsUnauthenticatedAndInvalidGroup(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", strings.NewReader(`{"name":"bad/name","members":[]}`))
	authorize(request, "admin-secret")
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, request)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"invalid_group"`) {
		t.Fatalf("invalid = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestResolverTokenCannotManageGroups(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", strings.NewReader(`{"name":"engineering","members":[{"name":"hosted","type":"hosted","endpoint":"test://available","position":0}]}`))
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"forbidden"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestResolverFailsWhenAuditCannotBeRecorded(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "group", Members: []repository.Member{{Name: "one", Type: repository.MemberHosted, Endpoint: "test://available", Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, failingAuditStore{store}, TestAdapter{}, testAuthenticator())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/group/resolve?repository=app", nil)
	authorize(request, "resolver-secret")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `resolver_error`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `outcome="failed"} 1`) {
		t.Fatalf("metrics = %s", metrics.Body.String())
	}
}

func TestMetricsReportResolverOutcomes(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "group", Members: []repository.Member{{Name: "one", Type: repository.MemberHosted, Endpoint: "http://gitea", Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/group/resolve?repository=app", nil)
	authorize(request, "resolver-secret")
	handler.ServeHTTP(response, request)
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `outcome="resolved"} 1`) {
		t.Fatalf("metrics = %s", metrics.Body.String())
	}
}

func TestMetricsCountDisabledAndMissingGroupsAsFailures(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "disabled", Members: []repository.Member{{Name: "one", Type: repository.MemberHosted, Endpoint: "test://available", Position: 0}}})
	if err := store.DisableGroup(context.Background(), "disabled"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	for _, target := range []string{"/api/v1/oci/groups/missing/resolve?repository=app", "/api/v1/oci/groups/disabled/resolve?repository=app"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		authorize(request, "resolver-secret")
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `outcome="failed"} 2`) {
		t.Fatalf("metrics = %s", metrics.Body.String())
	}
}
