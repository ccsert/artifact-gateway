package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestHostedRepositoryManagementLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"releases","format":"maven"}`))
	authorize(request, "admin-secret")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `"state":"active"`) {
		t.Fatalf("created=%s", created.Body.String())
	}
	id := strings.Split(strings.Split(created.Body.String(), `"id":"`)[1], `"`)[0]
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "releases") {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}
	disable := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+id, nil)
	authorize(disable, "admin-secret")
	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, disable)
	if disabled.Code != http.StatusAccepted || !strings.Contains(disabled.Body.String(), `"state":"pending"`) {
		t.Fatalf("disable=%d body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestHostedRepositoryManagementRejectsAnonymousAndInvalidRequests(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", denied.Code)
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"Bad Name","format":"npm"}`))
	authorize(bad, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, bad)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", response.Code, response.Body.String())
	}
	page := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageToken=unknown", nil)
	authorize(page, "admin-secret")
	paged := httptest.NewRecorder()
	handler.ServeHTTP(paged, page)
	if paged.Code != http.StatusBadRequest || !strings.Contains(paged.Body.String(), `"code":"invalid_page_token"`) {
		t.Fatalf("invalid page token=%d body=%s", paged.Code, paged.Body.String())
	}
}
