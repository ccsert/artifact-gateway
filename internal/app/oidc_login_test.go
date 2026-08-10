package app

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestOIDCBrowserLoginMapsBoundUserAndRevokesCookieSession(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const keyID = "browser-login-key"
	var expectedNonce string
	var provider *httptest.Server
	provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"issuer":                 provider.URL,
				"authorization_endpoint": provider.URL + "/authorize",
				"token_endpoint":         provider.URL + "/token",
				"jwks_uri":               provider.URL + "/jwks",
			})
		case "/jwks":
			_, _ = w.Write(oidcJWKS(t, keyID, &key.PublicKey))
		case "/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("code") != "authorization-code" || r.Form.Get("code_verifier") == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "provider-access-token",
				"token_type":   "Bearer",
				"expires_in":   300,
				"id_token": signedOIDCTokenWithClaims(
					t, key, keyID, provider.URL, "artifact-gateway-console", "gitlab-user", time.Now().Add(time.Minute), []string{"artifact-reader"}, expectedNonce,
				),
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer provider.Close()

	authenticator := testAuthenticator()
	authenticator.OIDC = NewOIDCValidator(OIDCConfig{
		Issuer: provider.URL, Audience: "artifact-gateway-api", JWKSURL: provider.URL + "/jwks",
		Roles: OIDCRoleMapping{Reader: []string{"artifact-reader"}},
	})
	dependencies := Dependencies{OIDCClient: NewOIDCClient(OIDCClientConfig{
		Issuer: provider.URL, ClientID: "artifact-gateway-console",
		RedirectURL: "http://localhost:4173/auth/oidc/callback", Scopes: []string{"openid", "profile"},
	}), OIDCLoginValidator: NewOIDCValidator(OIDCConfig{
		Issuer: provider.URL, Audience: "artifact-gateway-console", JWKSURL: provider.URL + "/jwks",
		Roles: OIDCRoleMapping{Reader: []string{"artifact-reader"}},
	})}
	store := repository.NewMemoryStore()
	localUser, err := store.CreateUser(t.Context(), repository.User{
		ID: "browser-oidc-user", Name: "local-user", Role: "writer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateUserIdentity(t.Context(), repository.UserIdentity{
		ID: "browser-oidc-identity", UserID: localUser.ID, Kind: repository.UserIdentityOIDC,
		Issuer: provider.URL, Subject: "gitlab-user",
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(dependencies, store, TestAdapter{}, authenticator)

	configResponse := httptest.NewRecorder()
	handler.ServeHTTP(configResponse, httptest.NewRequest(http.MethodGet, "/auth/oidc/config", nil))
	if configResponse.Code != http.StatusOK || !strings.Contains(configResponse.Body.String(), `"enabled":true`) || !strings.Contains(configResponse.Body.String(), `"issuer":"`+provider.URL+`"`) {
		t.Fatalf("config=%d body=%s", configResponse.Code, configResponse.Body.String())
	}

	startResponse := httptest.NewRecorder()
	handler.ServeHTTP(startResponse, httptest.NewRequest(http.MethodGet, "/auth/oidc/login?redirect=%2Frepositories", nil))
	if startResponse.Code != http.StatusFound {
		t.Fatalf("start=%d body=%s", startResponse.Code, startResponse.Body.String())
	}
	authorizationURL, err := url.Parse(startResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	expectedNonce = authorizationURL.Query().Get("nonce")
	state := authorizationURL.Query().Get("state")
	if expectedNonce == "" || state == "" || authorizationURL.Query().Get("code_challenge") == "" {
		t.Fatalf("authorization URL = %s", authorizationURL)
	}
	var flowCookie *http.Cookie
	for _, cookie := range startResponse.Result().Cookies() {
		if cookie.Name == oidcStateCookieName {
			flowCookie = cookie
		}
	}
	if flowCookie == nil || !flowCookie.HttpOnly || flowCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("flow cookie = %#v", flowCookie)
	}

	callback := httptest.NewRequest(http.MethodGet, "/auth/oidc/callback?code=authorization-code&state="+url.QueryEscape(state), nil)
	callback.AddCookie(flowCookie)
	callbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(callbackResponse, callback)
	if callbackResponse.Code != http.StatusFound || !strings.HasPrefix(callbackResponse.Header().Get("Location"), "/login?") {
		t.Fatalf("callback=%d location=%q body=%s", callbackResponse.Code, callbackResponse.Header().Get("Location"), callbackResponse.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, cookie := range callbackResponse.Result().Cookies() {
		if cookie.Name == webSessionCookieName && cookie.MaxAge > 0 {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %#v", sessionCookie)
	}

	identity := httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil)
	identity.AddCookie(sessionCookie)
	identityResponse := httptest.NewRecorder()
	handler.ServeHTTP(identityResponse, identity)
	if identityResponse.Code != http.StatusOK || !strings.Contains(identityResponse.Body.String(), `"actor":"user:local-user"`) || !strings.Contains(identityResponse.Body.String(), `"kind":"oidc"`) || !strings.Contains(identityResponse.Body.String(), `"role":"writer"`) {
		t.Fatalf("identity=%d body=%s", identityResponse.Code, identityResponse.Body.String())
	}

	current, err := store.GetUser(t.Context(), localUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.RevokeUserSessions(t.Context(), current.ID, current.Version); err != nil {
		t.Fatal(err)
	}
	revokedIdentity := httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil)
	revokedIdentity.AddCookie(sessionCookie)
	revokedResponse := httptest.NewRecorder()
	handler.ServeHTTP(revokedResponse, revokedIdentity)
	if revokedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("revoked cookie identity=%d body=%s", revokedResponse.Code, revokedResponse.Body.String())
	}
}

func TestOIDCConfigIsDisabledWithoutBrowserClient(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/oidc/config", nil))
	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"enabled":false}` {
		t.Fatalf("config=%d body=%s", response.Code, response.Body.String())
	}
}
