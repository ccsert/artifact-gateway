package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestAnonymousAccessPolicyManagementHTTP(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	request := func(method, body, token, version string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v2/anonymous-access-policy", strings.NewReader(body))
		if token != "" {
			authorize(r, token)
		}
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	if got := request(http.MethodGet, "", "", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated get = %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "", "writer-secret", ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin get = %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodGet, "", "admin-secret", ""); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"enabled":false`) || !strings.Contains(got.Body.String(), `"version":"1"`) {
		t.Fatalf("default policy = %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPut, `{"version":"1","enabled":true}`, "admin-secret", "1"); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"enabled":true`) || !strings.Contains(got.Body.String(), `"version":"2"`) {
		t.Fatalf("replace = %d %s", got.Code, got.Body.String())
	}
	if got := request(http.MethodPut, `{"version":"2","enabled":false}`, "admin-secret", "1"); got.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale replace = %d %s", got.Code, got.Body.String())
	}
}
