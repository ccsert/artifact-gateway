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
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
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
	runtime       *OIDCRuntime
	authenticator Authenticator
	identities    repository.UserIdentityStore
}

type oidcFlowState struct {
	State     string `json:"state"`
	Nonce     string `json:"nonce"`
	Verifier  string `json:"verifier"`
	Redirect  string `json:"redirect"`
	ExpiresAt int64  `json:"expiresAt"`
}

func (h oidcLoginHandler) config(w http.ResponseWriter, r *http.Request) {
	client, _, err := h.browser(r.Context())
	if errors.Is(err, errOIDCNotEnabled) || client == nil {
		writeNativeMavenJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "oidc_unavailable", "OIDC configuration is unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	metadata, err := client.Metadata(ctx)
	if err != nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "oidc_unavailable", "OIDC provider discovery failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, map[string]any{
		"enabled":          true,
		"issuer":           client.Issuer(),
		"clientId":         client.ClientID(),
		"redirectUrl":      client.RedirectURL(),
		"scopes":           client.Scopes(),
		"authorizationUrl": metadata.AuthorizationEndpoint,
	})
}

func (h oidcLoginHandler) start(w http.ResponseWriter, r *http.Request) {
	client, _, err := h.browser(r.Context())
	if errors.Is(err, errOIDCNotEnabled) || client == nil {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "OIDC browser login is not configured")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "oidc_unavailable", "OIDC configuration is unavailable")
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
	authorizationURL, err := client.AuthorizationURL(r.Context(), state, nonce, base64.RawURLEncoding.EncodeToString(challenge[:]))
	if err != nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "oidc_unavailable", "OIDC provider discovery failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookieName, Value: encoded, Path: "/auth/oidc", MaxAge: int(oidcFlowLifetime.Seconds()),
		HttpOnly: true, Secure: oidcSecureCookie(client.RedirectURL()), SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authorizationURL, http.StatusFound)
}

func (h oidcLoginHandler) callback(w http.ResponseWriter, r *http.Request) {
	client, validator, resolveErr := h.browser(r.Context())
	if resolveErr != nil || client == nil || validator == nil {
		h.redirectError(w, r, "not_configured")
		return
	}
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		h.clearFlowCookie(w, r)
		h.redirectError(w, r, providerError)
		return
	}
	code, state := r.URL.Query().Get("code"), r.URL.Query().Get("state")
	cookie, err := r.Cookie(oidcStateCookieName)
	if err != nil || code == "" || state == "" {
		h.clearFlowCookie(w, r)
		h.redirectError(w, r, "invalid_callback")
		return
	}
	flow, err := verifyOIDCFlow(cookie.Value, h.authenticator.AdminToken)
	if err != nil || flow.State != state || time.Now().UTC().Unix() >= flow.ExpiresAt {
		h.clearFlowCookie(w, r)
		h.redirectError(w, r, "invalid_state")
		return
	}
	h.clearFlowCookie(w, r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tokens, err := client.Exchange(ctx, code, flow.Verifier)
	if err != nil {
		h.redirectError(w, r, "exchange_failed")
		return
	}
	identity, ok := validator.ValidateNonce(ctx, tokens.IDToken, flow.Nonce)
	if !ok {
		h.redirectError(w, r, "invalid_identity")
		return
	}
	if h.identities != nil {
		settings := OIDCSettingsView{ProvisioningMode: "disabled", JITDefaultRole: "reader"}
		if h.runtime != nil {
			if current, err := h.runtime.Settings(ctx); err == nil {
				settings = current
			}
		}
		issuer := identity.Issuer
		if issuer == "" {
			issuer = client.Issuer()
		}
		user, _, _, resolveErr := h.identities.ResolveOIDCIdentity(ctx, repository.OIDCIdentityProvision{
			Issuer: issuer, Subject: identity.Subject, Email: identity.Email,
			DisplayName: identity.DisplayName, PreferredUsername: identity.Username,
			EmailVerified: identity.EmailVerified, Role: string(identity.Role),
			Provision: settings.ProvisioningMode == "jit", MatchEmail: settings.EmailLinkingEnabled,
			DefaultRole: settings.JITDefaultRole, OccurredAt: time.Now().UTC(),
		})
		switch {
		case resolveErr == nil:
			if user.State != repository.UserActive {
				h.redirectError(w, r, "account_disabled")
				return
			}
			principal := h.authenticator.PrincipalForActor("user:" + user.Name)
			principal.Admin = user.Role == string(authorization.RoleAdmin)
			principal.Role = authorization.Role(user.Role)
			principal.AuthenticationKind = authorization.AuthenticationOIDC
			principal.OIDCAdminSubject = identity.AdminSubject
			principal.OIDCRoleMappings = identity.RoleMappings
			session := h.authenticator.IssueWebSession(principal, user.SessionVersion)
			h.setWebSessionCookie(w, client, session)
			values := url.Values{"oidc": {"success"}, "redirect": {safeConsoleRedirect(flow.Redirect)}}
			http.Redirect(w, r, "/login?"+values.Encode(), http.StatusFound)
			return
		case errors.Is(resolveErr, repository.ErrNotFound):
			// Provisioning is disabled and the subject is not manually linked;
			// preserve the existing external-principal behavior.
		case errors.Is(resolveErr, repository.ErrIdentityAmbiguous):
			h.redirectError(w, r, "identity_ambiguous")
			return
		default:
			h.redirectError(w, r, "identity_unavailable")
			return
		}
	}
	principal := h.authenticator.PrincipalForActor(identity.Subject)
	principal.Admin = identity.Admin
	principal.Role = identity.Role
	principal.AuthenticationKind = authorization.AuthenticationOIDC
	principal.OIDCAdminSubject = identity.AdminSubject
	principal.OIDCRoleMappings = identity.RoleMappings
	session := h.authenticator.IssueWebSession(principal)
	h.setWebSessionCookie(w, client, session)
	values := url.Values{"oidc": {"success"}, "redirect": {safeConsoleRedirect(flow.Redirect)}}
	http.Redirect(w, r, "/login?"+values.Encode(), http.StatusFound)
}

func (h oidcLoginHandler) setWebSessionCookie(w http.ResponseWriter, client *authorization.OIDCClient, session string) {
	http.SetCookie(w, &http.Cookie{
		Name: webSessionCookieName, Value: session, Path: "/", MaxAge: int(webSessionLifetime.Seconds()),
		HttpOnly: true, Secure: oidcSecureCookie(client.RedirectURL()), SameSite: http.SameSiteLaxMode,
	})
}

func (h oidcLoginHandler) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: webSessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: oidcSecureRequest(r), SameSite: http.SameSiteLaxMode,
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

func (h oidcLoginHandler) clearFlowCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: oidcStateCookieName, Value: "", Path: "/auth/oidc", MaxAge: -1,
		HttpOnly: true, Secure: oidcSecureRequest(r), SameSite: http.SameSiteLaxMode,
	})
}

func (h oidcLoginHandler) browser(ctx context.Context) (*authorization.OIDCClient, *authorization.OIDCValidator, error) {
	if h.runtime != nil {
		return h.runtime.Browser(ctx)
	}
	if h.client == nil || h.validator == nil {
		return nil, nil, errOIDCNotEnabled
	}
	return h.client, h.validator, nil
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

func oidcSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
