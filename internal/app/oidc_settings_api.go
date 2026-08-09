package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
)

func (h generatedRepositoryAPIAdapter) GetOIDCSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	if h.oidcRuntime == nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "not_supported", "runtime OIDC settings are unavailable")
		return
	}
	settings, err := h.oidcRuntime.Settings(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "oidc_unavailable", "load OIDC settings failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, oidcSettingsResponse(settings))
}

func (h generatedRepositoryAPIAdapter) ReplaceOIDCSettings(w http.ResponseWriter, r *http.Request, params adminopenapi.ReplaceOIDCSettingsParams) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	if h.oidcRuntime == nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "not_supported", "runtime OIDC settings are unavailable")
		return
	}
	var request adminopenapi.OIDCSettingsUpdate
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "OIDC settings request is invalid")
		return
	}
	jwksURL := ""
	if request.JwksUrl != nil {
		jwksURL = *request.JwksUrl
	}
	settings, err := h.oidcRuntime.Replace(r.Context(), OIDCSettingsUpdate{
		Enabled: request.Enabled, Issuer: request.Issuer, Audience: request.Audience,
		JWKSURL: jwksURL, ClientID: request.ClientId, ClientSecret: request.ClientSecret,
		ClearClientSecret: request.ClearClientSecret != nil && *request.ClearClientSecret,
		RedirectURL:       request.RedirectUrl, Scopes: request.Scopes, AdminSubjects: request.AdminSubjects,
		ReaderRoles: request.ReaderRoles, WriterRoles: request.WriterRoles, AdminRoles: request.AdminRoles,
	}, string(params.IfMatch))
	switch {
	case errors.Is(err, repository.ErrVersionConflict):
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match the current OIDC settings version")
		return
	case errors.Is(err, secrets.ErrKeyNotConfigured), errors.Is(err, secrets.ErrInvalidKey):
		writeHostedProblem(w, http.StatusServiceUnavailable, "encryption_key_unavailable", secrets.KeyEnv+" is required to persist OIDC credentials")
		return
	case errors.Is(err, errInvalidOIDCSettings):
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	case err != nil:
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "replace OIDC settings failed")
		return
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
			Actor: principal.Actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(),
			Format: "management", Resource: "authentication/oidc", Operation: "authentication.oidc.configure",
			Status: http.StatusOK, CacheDisposition: "bypass",
		})
	}
	writeNativeMavenJSON(w, http.StatusOK, oidcSettingsResponse(settings))
}

func (h generatedRepositoryAPIAdapter) TestOIDCSettings(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	if h.oidcRuntime == nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "not_supported", "runtime OIDC settings are unavailable")
		return
	}
	result, err := h.oidcRuntime.Test(r.Context())
	status := http.StatusOK
	outcome := repository.AuditResolved
	if errors.Is(err, errOIDCNotEnabled) {
		status = http.StatusBadRequest
		outcome = repository.AuditAccessDenied
	} else if err != nil {
		status = http.StatusServiceUnavailable
		outcome = repository.AuditUpstreamError
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
			Actor: principal.Actor, Outcome: outcome, OccurredAt: time.Now().UTC(), Format: "management",
			Resource: "authentication/oidc", Operation: "authentication.oidc.test", Status: status, CacheDisposition: "bypass",
		})
	}
	if err != nil {
		message := "OIDC provider discovery failed"
		if errors.Is(err, errOIDCNotEnabled) {
			message = "OIDC authentication is not enabled"
		}
		writeHostedProblem(w, status, "oidc_unavailable", message)
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.OIDCConnectionTest{
		Reachable: result.Reachable, Issuer: result.Issuer,
		AuthorizationEndpoint: optionalString(result.AuthorizationEndpoint),
		TokenEndpoint:         optionalString(result.TokenEndpoint), JwksUrl: optionalString(result.JWKSURL),
		LatencyMs: int(result.LatencyMs), CheckedAt: result.CheckedAt,
	})
}

func oidcSettingsResponse(settings OIDCSettingsView) adminopenapi.OIDCSettings {
	response := adminopenapi.OIDCSettings{
		Version: settings.Version, Source: adminopenapi.OIDCSettingsSource(settings.Source), Enabled: settings.Enabled,
		Issuer: settings.Issuer, Audience: settings.Audience, JwksUrl: optionalString(settings.JWKSURL),
		ClientId: settings.ClientID, ClientSecretConfigured: settings.ClientSecretConfigured,
		RedirectUrl: settings.RedirectURL, Scopes: settings.Scopes, AdminSubjects: settings.AdminSubjects,
		ReaderRoles: settings.ReaderRoles, WriterRoles: settings.WriterRoles, AdminRoles: settings.AdminRoles,
	}
	if !settings.UpdatedAt.IsZero() {
		response.UpdatedAt = &settings.UpdatedAt
	}
	return response
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
