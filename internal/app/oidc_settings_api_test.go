package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
)

func TestOIDCRuntimeSupportsBearerOnlyBootstrap(t *testing.T) {
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": provider.URL, "authorization_endpoint": provider.URL + "/authorize",
			"token_endpoint": provider.URL + "/token", "jwks_uri": provider.URL + "/jwks",
		})
	}))
	defer provider.Close()

	runtime := NewOIDCRuntime(repository.NewMemoryStore(), OIDCRuntimeConfig{
		Enabled: true, Issuer: provider.URL, Audience: "artifact-gateway-api",
	})
	settings, err := runtime.Settings(t.Context())
	if err != nil || !settings.Enabled || settings.ClientID != "" || settings.RedirectURL != "" {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
	if client, validator, err := runtime.Browser(t.Context()); client != nil || validator != nil || !errors.Is(err, errOIDCNotEnabled) {
		t.Fatalf("browser client=%#v validator=%#v err=%v", client, validator, err)
	}
	validator, err := runtime.OIDCValidator(t.Context())
	if err != nil || validator == nil {
		t.Fatalf("API validator=%#v err=%v", validator, err)
	}
	result, err := runtime.Test(t.Context())
	if err != nil || !result.Reachable || result.JWKSURL != provider.URL+"/jwks" {
		t.Fatalf("connection test=%#v err=%v", result, err)
	}
}

func TestOIDCSettingsRequireCompleteBrowserConfiguration(t *testing.T) {
	_, err := normalizeOIDCSettingsUpdate(OIDCSettingsUpdate{
		Enabled: true, Issuer: "https://identity.example.test", Audience: "artifact-gateway-api",
		ClientID: "artifact-gateway-console",
	})
	if err == nil || !strings.Contains(err.Error(), "clientId and redirectUrl") {
		t.Fatalf("partial browser configuration err=%v", err)
	}

	settings, err := normalizeOIDCSettingsUpdate(OIDCSettingsUpdate{
		Enabled: true, Issuer: "https://identity.example.test", Audience: "artifact-gateway-api",
	})
	if err != nil || !settings.Enabled {
		t.Fatalf("bearer-only settings=%#v err=%v", settings, err)
	}
}

func TestOIDCSettingsCanMoveFromEnvironmentToRuntimeStorage(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": provider.URL, "authorization_endpoint": provider.URL + "/authorize",
			"token_endpoint": provider.URL + "/token", "jwks_uri": provider.URL + "/jwks",
		})
	}))
	defer provider.Close()

	store := repository.NewMemoryStore()
	runtime := NewOIDCRuntime(store, OIDCRuntimeConfig{})
	handler := NewGatewayHandler(Dependencies{OIDCRuntime: runtime}, store, TestAdapter{}, testAuthenticator())

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v2/authentication/oidc", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}

	initial := httptest.NewRequest(http.MethodGet, "/api/v2/authentication/oidc", nil)
	authorize(initial, "admin-secret")
	initialResponse := httptest.NewRecorder()
	handler.ServeHTTP(initialResponse, initial)
	if initialResponse.Code != http.StatusOK || !strings.Contains(initialResponse.Body.String(), `"version":"0"`) || !strings.Contains(initialResponse.Body.String(), `"source":"environment"`) || !strings.Contains(initialResponse.Body.String(), `"adminRoles":[]`) {
		t.Fatalf("initial=%d body=%s", initialResponse.Code, initialResponse.Body.String())
	}

	body := `{"enabled":true,"issuer":"` + provider.URL + `","audience":"artifact-gateway-api","clientId":"artifact-gateway-console","clientSecret":"runtime-secret","redirectUrl":"http://localhost:4173/auth/oidc/callback","scopes":["openid","profile"],"adminSubjects":[],"readerRoles":["artifact-reader"],"writerRoles":["artifact-writer"],"adminRoles":["artifact-admin"]}`
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/authentication/oidc", strings.NewReader(body))
	replace.Header.Set("If-Match", "0")
	authorize(replace, "admin-secret")
	replaceResponse := httptest.NewRecorder()
	handler.ServeHTTP(replaceResponse, replace)
	if replaceResponse.Code != http.StatusOK || !strings.Contains(replaceResponse.Body.String(), `"version":"1"`) || !strings.Contains(replaceResponse.Body.String(), `"source":"database"`) || !strings.Contains(replaceResponse.Body.String(), `"clientSecretConfigured":true`) || strings.Contains(replaceResponse.Body.String(), "runtime-secret") {
		t.Fatalf("replace=%d body=%s", replaceResponse.Code, replaceResponse.Body.String())
	}
	stored, err := store.GetOIDCSettings(replace.Context())
	if err != nil || stored.ClientSecret == "" || stored.ClientSecret == "runtime-secret" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}

	publicConfig := httptest.NewRecorder()
	handler.ServeHTTP(publicConfig, httptest.NewRequest(http.MethodGet, "/auth/oidc/config", nil))
	if publicConfig.Code != http.StatusOK || !strings.Contains(publicConfig.Body.String(), `"enabled":true`) || !strings.Contains(publicConfig.Body.String(), provider.URL) {
		t.Fatalf("public config=%d body=%s", publicConfig.Code, publicConfig.Body.String())
	}

	testRequest := httptest.NewRequest(http.MethodPost, "/api/v2/authentication/oidc:test", nil)
	authorize(testRequest, "admin-secret")
	testResponse := httptest.NewRecorder()
	handler.ServeHTTP(testResponse, testRequest)
	if testResponse.Code != http.StatusOK || !strings.Contains(testResponse.Body.String(), `"reachable":true`) || !strings.Contains(testResponse.Body.String(), `"jwksUrl":"`+provider.URL+`/jwks"`) {
		t.Fatalf("test=%d body=%s", testResponse.Code, testResponse.Body.String())
	}

	conflict := httptest.NewRequest(http.MethodPut, "/api/v2/authentication/oidc", strings.NewReader(body))
	conflict.Header.Set("If-Match", "0")
	authorize(conflict, "admin-secret")
	conflictResponse := httptest.NewRecorder()
	handler.ServeHTTP(conflictResponse, conflict)
	if conflictResponse.Code != http.StatusPreconditionFailed {
		t.Fatalf("conflict=%d body=%s", conflictResponse.Code, conflictResponse.Body.String())
	}

	operations := map[string]bool{}
	for _, audit := range store.Audits {
		operations[audit.Operation] = true
	}
	if !operations["authentication.oidc.configure"] || !operations["authentication.oidc.test"] {
		t.Fatalf("audits=%#v", store.Audits)
	}
}

func TestOIDCSettingsSecretIsPreservedWhenOmitted(t *testing.T) {
	t.Setenv(secrets.KeyEnv, "0123456789abcdef0123456789abcdef")
	store := repository.NewMemoryStore()
	runtime := NewOIDCRuntime(store, OIDCRuntimeConfig{ClientSecret: "environment-secret"})
	first, err := runtime.Replace(t.Context(), OIDCSettingsUpdate{}, "0")
	if err != nil || !first.ClientSecretConfigured {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := runtime.Replace(t.Context(), OIDCSettingsUpdate{}, "1")
	if err != nil || !second.ClientSecretConfigured || second.Version != "2" {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	stored, err := store.GetOIDCSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	opened, err := secrets.Open(oidcClientSecretPurpose, stored.ClientSecret)
	if err != nil || opened != "environment-secret" {
		t.Fatalf("opened=%q err=%v", opened, err)
	}

	replacement := "replacement-secret"
	third, err := runtime.Replace(t.Context(), OIDCSettingsUpdate{
		ClientSecret: &replacement, ClearClientSecret: true,
	}, "2")
	if err != nil || !third.ClientSecretConfigured || third.Version != "3" {
		t.Fatalf("third=%#v err=%v", third, err)
	}
	stored, err = store.GetOIDCSettings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	opened, err = secrets.Open(oidcClientSecretPurpose, stored.ClientSecret)
	if err != nil || opened != replacement {
		t.Fatalf("replacement opened=%q err=%v", opened, err)
	}
}
