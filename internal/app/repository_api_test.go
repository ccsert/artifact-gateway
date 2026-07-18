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

type selectiveAdapter struct{ available map[string]bool }

func (a selectiveAdapter) Available(_ context.Context, member repository.Member, _ string) bool {
	return a.available[member.Name]
}

func TestGroupManagementAndResolverVerticalSlice(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, selectiveAdapter{available: map[string]bool{"proxy": true}}, "secret")
	group := `{"name":"engineering","members":[{"name":"hosted","type":"hosted","endpoint":"http://gitea","position":0},{"name":"proxy","type":"proxy","endpoint":"https://registry.example","position":1}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", strings.NewReader(group))
	request.Header.Set("Authorization", "Bearer secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", created.Code, created.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/engineering", nil)
	request.Header.Set("Authorization", "Bearer secret")
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
	request.Header.Set("Authorization", "Bearer secret")
	resolved := httptest.NewRecorder()
	handler.ServeHTTP(resolved, request)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"name":"proxy"`) {
		t.Fatalf("resolve = %d %s", resolved.Code, resolved.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Outcome != "resolved" || store.Audits[0].MemberName != "proxy" {
		t.Fatalf("audits = %#v", store.Audits)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups/engineering/disable", nil)
	request.Header.Set("Authorization", "Bearer secret")
	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, request)
	if disabled.Code != http.StatusNoContent {
		t.Fatalf("disable status = %d", disabled.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/engineering/resolve?repository=team/app", nil)
	request.Header.Set("Authorization", "Bearer secret")
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, request)
	if blocked.Code != http.StatusConflict || !strings.Contains(blocked.Body.String(), `group_disabled`) {
		t.Fatalf("disabled resolve = %d %s", blocked.Code, blocked.Body.String())
	}
}

func TestAPIRejectsUnauthenticatedAndInvalidGroup(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, "secret")
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", unauthenticated.Code)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/oci/groups", strings.NewReader(`{"name":"bad/name","members":[]}`))
	request.Header.Set("Authorization", "Bearer secret")
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, request)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), `"code":"invalid_group"`) {
		t.Fatalf("invalid = %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestMetricsReportResolverOutcomes(t *testing.T) {
	store := repository.NewMemoryStore()
	_, _ = store.CreateGroup(context.Background(), repository.Group{Name: "group", Members: []repository.Member{{Name: "one", Type: repository.MemberHosted, Endpoint: "http://gitea", Position: 0}}})
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, "")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/oci/groups/group/resolve?repository=app", nil))
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `outcome="resolved"} 1`) {
		t.Fatalf("metrics = %s", metrics.Body.String())
	}
}
