package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
)

const (
	oidcStateCookieName  = "ag_oidc_state"
	webSessionCookieName = "ag_session"
	oidcFlowLifetime     = 10 * time.Minute
	webSessionLifetime   = 12 * time.Hour
)

type oidcLoginHandler struct {
	client        *authorization.OIDCClient
	validator     *authorization.OIDCValidator
	authenticator Authenticator
}

type oidcFlowState struct {
	State     string `json:"state"`
	Nonce     string `json:"nonce"`
	Verifier  string `json:"verifier"`
	Redirect  string `json:"redirect"`
	ExpiresAt int64  `json:"expiresAt"`
}

func (h oidcLoginHandler) config(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		writeNativeMavenJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	metadata, err := h.client.Metadata(ctx)
	if err != nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "oidc_unavailable", "OIDC provider discovery failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, map[string]any{
		"enabled":          true,
		"issuer":           h.client.Issuer(),
		"clientId":         h.client.ClientID(),
		"redirectUrl":      h.client.RedirectURL(),
		"scopes":           h.client.Scopes(),
		"authorizationUrl": metadata.AuthorizationEndpoint,
	})
}

func (h oidcLoginHandler) start(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "OIDC browser login is not configured")
		return
	}
	state, err := randomURLToken(32)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create OIDC login state failed")
		return
	}
	nonce, err := randomURLToken(32)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create OIDC login nonce failed")
		return
	}
	verifier, err := randomURLToken(48)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create OIDC PKCE verifier failed")
		return
	}
	flow := oidcFlowState{
		State: state, Nonce: nonce, Verifier: verifier,
		Redirect:  safeConsoleRedirect(r.URL.Query().Get("redirect")),
		ExpiresAt: time.Now().UTC().Add(oidcFlowLifetime).Unix(),
	}
	encoded, err := signOIDCFlow(flow, h.authenticator.AdminToken)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create OIDC login state failed")
		return
	}
	challenge := sha256.Sum256([]byte(verifier))
	authorizationURL, err := h.client.AuthorizationURL(r.Context(), state, nonce, base64.RawURLEncoding.EncodeToString(challenge[:]))
	if err != nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "oidc_unavailable", "OIDC provider discovery failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookieName, Value: encoded, Path: "/auth/oidc", MaxAge: int(oidcFlowLifetime.Seconds()),
		HttpOnly: true, Secure: oidcSecureCookie(h.client.RedirectURL()), SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

func (h oidcLoginHandler) callback(w http.ResponseWriter, r *http.Request) {
	if h.client == nil {
		h.redirectError(w, r, "not_configured")
		return
	}
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		h.clearFlowCookie(w)
		h.redirectError(w, r, providerError)
		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	cookie, err := r.Cookie(oidcStateCookieName)
	if err != nil || code == "" || state == "" {
		h.clearFlowCookie(w)
		h.redirectError(w, r, "invalid_callback")
		return
	}
	flow, err := verifyOIDCFlow(cookie.Value, h.authenticator.AdminToken)
	if err != nil || flow.State != state || time.Now().UTC().Unix() >= flow.ExpiresAt {
		h.clearFlowCookie(w)
		h.redirectError(w, r, "invalid_state")
		return
	}
	h.clearFlowCookie(w)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tokens, err := h.client.Exchange(ctx, code, flow.Verifier)
	if err != nil || h.validator == nil {
		h.redirectError(w, r, "exchange_failed")
		return
	}
	identity, ok := h.validator.ValidateNonce(ctx, tokens.IDToken, flow.Nonce)
	if !ok {
		h.redirectError(w, r, "invalid_identity")
		return
	}
	principal := h.authenticator.PrincipalForActor(identity.Subject)
	principal.Admin = identity.Admin
	principal.Role = identity.Role
	principal.AuthenticationKind = authorization.AuthenticationOIDC
	principal.OIDCAdminSubject = identity.AdminSubject
	principal.OIDCRoleMappings = identity.RoleMappings
	session := h.authenticator.IssueWebSession(principal)
	http.SetCookie(w, &http.Cookie{
		Name: webSessionCookieName, Value: session, Path: "/", MaxAge: int(webSessionLifetime.Seconds()),
		HttpOnly: true, Secure: oidcSecureCookie(h.client.RedirectURL()), SameSite: http.SameSiteLaxMode,
	})
	values := url.Values{"oidc": {"success"}, "redirect": {safeConsoleRedirect(flow.Redirect)}}
	http.Redirect(w, r, "/login?"+values.Encode(), http.StatusFound)
}

func (h oidcLoginHandler) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: webSessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: h.client != nil && oidcSecureCookie(h.client.RedirectURL()), SameSite: http.SameSiteLaxMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h oidcLoginHandler) session(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeNativeMavenJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"identity":      currentIdentityResponse(principal),
	})
}

func (h oidcLoginHandler) redirectError(w http.ResponseWriter, r *http.Request, code string) {
	values := url.Values{"oidc_error": {code}}
	http.Redirect(w, r, "/login?"+values.Encode(), http.StatusFound)
}

func (h oidcLoginHandler) clearFlowCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookieName, Value: "", Path: "/auth/oidc", MaxAge: -1,
		HttpOnly: true, Secure: h.client != nil && oidcSecureCookie(h.client.RedirectURL()), SameSite: http.SameSiteLaxMode,
	})
}

func sessionCookieAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if cookie, err := r.Cookie(webSessionCookieName); err == nil && cookie.Value != "" {
				clone := r.Clone(r.Context())
				clone.Header.Set("Authorization", "Bearer "+cookie.Value)
				r = clone
			}
		}
		next.ServeHTTP(w, r)
	})
}

func randomURLToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func signOIDCFlow(flow oidcFlowState, secret string) (string, error) {
	if secret == "" {
		return "", errors.New("OIDC state signing secret is empty")
	}
	payload, err := json.Marshal(flow)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyOIDCFlow(value, secret string) (oidcFlowState, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || secret == "" {
		return oidcFlowState{}, errors.New("invalid OIDC state")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return oidcFlowState{}, errors.New("invalid OIDC state signature")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return oidcFlowState{}, errors.New("invalid OIDC state signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return oidcFlowState{}, errors.New("invalid OIDC state payload")
	}
	var flow oidcFlowState
	if json.Unmarshal(payload, &flow) != nil || flow.State == "" || flow.Nonce == "" || flow.Verifier == "" || flow.ExpiresAt == 0 {
		return oidcFlowState{}, errors.New("invalid OIDC state payload")
	}
	return flow, nil
}

func safeConsoleRedirect(value string) string {
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return "/"
	}
	return value
}

func oidcSecureCookie(redirectURL string) bool {
	parsed, err := url.Parse(redirectURL)
	return err == nil && parsed.Scheme == "https"
}
