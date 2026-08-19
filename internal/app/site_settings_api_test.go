package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestSiteSettingsArePubliclyReadableAndAdminVersioned(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/v2/site-settings", nil))
	if read.Code != http.StatusOK || read.Header().Get("ETag") != "1" || !strings.Contains(read.Body.String(), `"siteName":"Artifact Gateway"`) || !strings.Contains(read.Body.String(), `"id":"aerok-dark"`) {
		t.Fatalf("public read=%d etag=%q body=%s", read.Code, read.Header().Get("ETag"), read.Body.String())
	}

	unauthorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v2/site-settings", strings.NewReader(`{"siteName":"Acme Packages","logoUrl":"","brandMark":"AC","enabledThemeIds":["aerok-dark","aerok-light"],"defaultThemeId":"aerok-dark"}`))
	request.Header.Set("If-Match", "1")
	handler.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized replace=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	replace := httptest.NewRequest(http.MethodPut, "/api/v2/site-settings", strings.NewReader(`{"siteName":"Acme Packages","logoUrl":"/assets/acme.webp","brandMark":"AC","enabledThemeIds":["aerok-dark","aerok-light"],"defaultThemeId":"aerok-light"}`))
	replace.Header.Set("If-Match", "1")
	authorize(replace, "admin-secret")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != "2" || !strings.Contains(replaced.Body.String(), `"brandMark":"AC"`) {
		t.Fatalf("replace=%d etag=%q body=%s", replaced.Code, replaced.Header().Get("ETag"), replaced.Body.String())
	}
	if len(store.Audits) != 1 || store.Audits[0].Operation != "site-settings.replace" {
		t.Fatalf("audits=%#v", store.Audits)
	}

	stale := httptest.NewRequest(http.MethodPut, "/api/v2/site-settings", strings.NewReader(`{"siteName":"Stale","logoUrl":"","brandMark":"ST","enabledThemeIds":["aerok-dark"],"defaultThemeId":"aerok-dark"}`))
	stale.Header.Set("If-Match", "1")
	authorize(stale, "admin-secret")
	staleResponse := httptest.NewRecorder()
	handler.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale replace=%d body=%s", staleResponse.Code, staleResponse.Body.String())
	}
}

func TestSiteSettingsRejectUnavailableOrDisabledDefaultTheme(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	for name, body := range map[string]string{
		"unavailable":      `{"siteName":"Acme","logoUrl":"","brandMark":"AC","enabledThemeIds":["missing-theme"],"defaultThemeId":"missing-theme"}`,
		"disabled default": `{"siteName":"Acme","logoUrl":"","brandMark":"AC","enabledThemeIds":["aerok-dark"],"defaultThemeId":"aerok-light"}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/api/v2/site-settings", strings.NewReader(body))
			request.Header.Set("If-Match", "1")
			authorize(request, "admin-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSiteSettingsRejectUnsafeLogoURLs(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPut, "/api/v2/site-settings", strings.NewReader(`{"siteName":"Acme","logoUrl":"javascript:alert(1)","brandMark":"AC","enabledThemeIds":["aerok-dark"],"defaultThemeId":"aerok-dark"}`))
	request.Header.Set("If-Match", "1")
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "logoUrl") {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
}
