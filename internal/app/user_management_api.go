package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

func (h generatedRepositoryAPIAdapter) ListApiKeys(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	keys, err := h.apiKeys.ListAPIKeys(r.Context())
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list API keys failed")
		return
	}
	items := make([]adminopenapi.APIKey, 0, len(keys))
	for _, key := range keys {
		items = append(items, apiKeyResponse(key))
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.APIKeyList{Items: items})
}

func (h generatedRepositoryAPIAdapter) CreateApiKey(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	var request adminopenapi.CreateAPIKey
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.Name) == "" {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	now := time.Now().UTC()
	expiresAt := now.Add(90 * 24 * time.Hour)
	if request.ExpiresAt != nil {
		expiresAt = request.ExpiresAt.UTC()
	}
	if !expiresAt.After(now) || expiresAt.After(now.Add(365*24*time.Hour)) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "API key expiry must be in the future and no more than 365 days away")
		return
	}
	roles := make([]string, 0)
	if request.Roles != nil {
		roles = make([]string, 0, len(*request.Roles))
	}
	for _, role := range valueOrEmptyRoles(request.Roles) {
		switch role {
		case adminopenapi.CreateAPIKeyRolesAdmin, adminopenapi.CreateAPIKeyRolesWriter, adminopenapi.CreateAPIKeyRolesReader:
		default:
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "unsupported API key role")
			return
		}
		roles = append(roles, string(role))
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "generate API key failed")
		return
	}
	token := "agk_" + base64.RawURLEncoding.EncodeToString(raw)
	key, err := h.apiKeys.CreateAPIKey(r.Context(), repository.APIKey{ID: uuid.NewString(), Name: strings.TrimSpace(request.Name), SecretHash: authorization.HashAPIKey(token), Roles: roles, ExpiresAt: &expiresAt})
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create API key failed")
		return
	}
	response := adminopenapi.CreatedAPIKey{Id: uuid.MustParse(key.ID), Name: key.Name, CreatedAt: key.CreatedAt, ExpiresAt: key.ExpiresAt, LastUsedAt: key.LastUsedAt, Token: token, Roles: make([]adminopenapi.CreatedAPIKeyRoles, 0, len(key.Roles))}
	for _, role := range key.Roles {
		response.Roles = append(response.Roles, adminopenapi.CreatedAPIKeyRoles(role))
	}
	writeNativeMavenJSON(w, http.StatusCreated, response)
}

func valueOrEmptyRoles(roles *[]adminopenapi.CreateAPIKeyRoles) []adminopenapi.CreateAPIKeyRoles {
	if roles == nil {
		return nil
	}
	return *roles
}

func (h generatedRepositoryAPIAdapter) RevokeApiKey(w http.ResponseWriter, r *http.Request, apiKeyID uuid.UUID) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	key, err := h.apiKeys.RevokeAPIKey(r.Context(), apiKeyID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "API key not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "revoke API key failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, apiKeyResponse(key))
}

func apiKeyResponse(key repository.APIKey) adminopenapi.APIKey {
	response := adminopenapi.APIKey{Id: uuid.MustParse(key.ID), Name: key.Name, CreatedAt: key.CreatedAt, RevokedAt: key.RevokedAt, ExpiresAt: key.ExpiresAt, LastUsedAt: key.LastUsedAt, Roles: make([]adminopenapi.APIKeyRoles, 0, len(key.Roles))}
	for _, role := range key.Roles {
		response.Roles = append(response.Roles, adminopenapi.APIKeyRoles(role))
	}
	return response
}

func userResponse(user repository.User) adminopenapi.User {
	response := adminopenapi.User{
		CreatedAt: user.CreatedAt, Id: uuid.MustParse(user.ID), Name: user.Name,
		DisplayName: user.DisplayName, Email: user.Email, Description: user.Description,
		Role: adminopenapi.UserRole(user.Role), State: adminopenapi.UserState(user.State),
		LastLoginAt: user.LastLoginAt, PasswordChangedAt: user.PasswordChangedAt,
		FailedLoginAttempts: user.FailedLoginAttempts, LockedUntil: user.LockedUntil,
		MustChangePassword: user.MustChangePassword, LocalPasswordEnabled: user.SecretHash != "", Version: user.Version,
	}
	if !user.UpdatedAt.IsZero() {
		updated := user.UpdatedAt
		response.UpdatedAt = &updated
	}
	return response
}

func userIdentityResponse(identity repository.UserIdentity) adminopenapi.UserIdentity {
	response := adminopenapi.UserIdentity{
		Id: uuid.MustParse(identity.ID), UserId: uuid.MustParse(identity.UserID),
		Kind: adminopenapi.UserIdentityKind(identity.Kind), Issuer: identity.Issuer,
		Subject: identity.Subject, Email: identity.Email, DisplayName: identity.DisplayName,
		EmailVerified: identity.EmailVerified, CreatedAt: identity.CreatedAt,
	}
	if identity.LastLoginAt != nil {
		lastLogin := *identity.LastLoginAt
		response.LastLoginAt = &lastLogin
	}
	if !identity.UpdatedAt.IsZero() {
		updated := identity.UpdatedAt
		response.UpdatedAt = &updated
	}
	return response
}

func userSessionResponse(session repository.UserSession, current bool) adminopenapi.UserSession {
	return adminopenapi.UserSession{
		Id: uuid.MustParse(session.ID), UserId: uuid.MustParse(session.UserID),
		Kind: adminopenapi.UserSessionKind(session.Kind), IpAddress: session.IPAddress,
		UserAgent: session.UserAgent, CreatedAt: session.CreatedAt, ExpiresAt: session.ExpiresAt,
		RevokedAt: session.RevokedAt, Current: current,
	}
}

func validUserRole(role string) bool {
	return role == string(authorization.RoleAdmin) || role == string(authorization.RoleWriter) || role == string(authorization.RoleReader)
}

const maxLocalPasswordBytes = 72 // bcrypt's maximum effective password length.

func validUserText(value string, maxRunes int) bool {
	return utf8.RuneCountInString(value) <= maxRunes
}

func validUserEmail(value string) bool {
	if value == "" || !validUserText(value, 254) {
		return value == ""
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}

func validLocalPassword(value string) bool {
	return utf8.RuneCountInString(value) >= 8 && len(value) <= maxLocalPasswordBytes
}

func (h generatedRepositoryAPIAdapter) ListUsers(w http.ResponseWriter, r *http.Request, params adminopenapi.ListUsersParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	query := repository.UserListQuery{Limit: 50}
	if params.Search != nil {
		query.Search = strings.TrimSpace(*params.Search)
	}
	if params.Role != nil {
		query.Role = string(*params.Role)
	}
	if params.State != nil {
		query.State = string(*params.State)
	}
	if params.Limit != nil {
		query.Limit = *params.Limit
	}
	if params.Offset != nil {
		query.Offset = *params.Offset
	}
	if query.Limit < 1 || query.Limit > 200 || query.Offset < 0 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "limit must be between 1 and 200 and offset must not be negative")
		return
	}
	page, err := h.users.ListUsers(r.Context(), query)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list users failed")
		return
	}
	items := make([]adminopenapi.User, 0, len(page.Items))
	for _, user := range page.Items {
		items = append(items, userResponse(user))
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.UserList{Items: items, Total: page.Total, Offset: page.Offset, Limit: page.Limit})
}

func (h generatedRepositoryAPIAdapter) CreateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	var request adminopenapi.CreateUser
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.Name) == "" || !validLocalPassword(request.Password) || !validUserRole(string(request.Role)) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "name, a password of at least 8 characters and at most 72 bytes, and a valid role are required")
		return
	}
	name := strings.TrimSpace(request.Name)
	displayName, email, description := "", "", ""
	if request.DisplayName != nil {
		displayName = strings.TrimSpace(*request.DisplayName)
	}
	if request.Email != nil {
		email = strings.TrimSpace(string(*request.Email))
	}
	if request.Description != nil {
		description = strings.TrimSpace(*request.Description)
	}
	if !validUserText(name, 128) || !validUserText(displayName, 128) || !validUserEmail(email) || !validUserText(description, 512) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "user profile fields exceed their limits or email is invalid")
		return
	}
	hash, err := authorization.HashPassword(request.Password)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "hash password failed")
		return
	}
	mustChange := request.MustChangePassword != nil && *request.MustChangePassword
	user, err := h.users.CreateUser(r.Context(), repository.User{ID: uuid.NewString(), Name: name, DisplayName: displayName, Email: email, Description: description, SecretHash: hash, Role: string(request.Role), MustChangePassword: mustChange})
	if errors.Is(err, repository.ErrNameExists) {
		writeHostedProblem(w, http.StatusConflict, "version_conflict", "user name already exists")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create user failed")
		return
	}
	h.recordUserManagementAudit(r, user, "user.create", http.StatusCreated)
	writeNativeMavenJSON(w, http.StatusCreated, userResponse(user))
}

func (h generatedRepositoryAPIAdapter) GetUser(w http.ResponseWriter, r *http.Request, userID string) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	user, err := h.users.GetUser(r.Context(), userID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get user failed")
		return
	}
	writeNativeMavenJSON(w, http.StatusOK, userResponse(user))
}

func (h generatedRepositoryAPIAdapter) UpdateUser(w http.ResponseWriter, r *http.Request, userID string, params adminopenapi.UpdateUserParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	var request adminopenapi.UpdateUser
	if err := decoder.Decode(&request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "invalid user update body")
		return
	}
	role := ""
	if request.Role != nil {
		role = string(*request.Role)
	}
	if role != "" && !validUserRole(role) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "unsupported user role")
		return
	}
	state := ""
	if request.State != nil {
		state = string(*request.State)
	}
	if state != "" && state != repository.UserActive && state != repository.UserDisabled {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "unsupported user state")
		return
	}
	if role == "" && state == "" {
		if request.DisplayName == nil && request.Email == nil && request.Description == nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "provide a profile, role, or state to update")
			return
		}
	}
	profile := repository.UserUpdate{ID: userID}
	if request.Role != nil {
		profile.Role = &role
	}
	if request.State != nil {
		profile.State = &state
	}
	if request.DisplayName != nil {
		value := strings.TrimSpace(*request.DisplayName)
		profile.DisplayName = &value
	}
	if request.Email != nil {
		value := strings.TrimSpace(string(*request.Email))
		profile.Email = &value
	}
	if request.Description != nil {
		value := strings.TrimSpace(*request.Description)
		profile.Description = &value
	}
	if (profile.DisplayName != nil && !validUserText(*profile.DisplayName, 128)) || (profile.Email != nil && !validUserEmail(*profile.Email)) || (profile.Description != nil && !validUserText(*profile.Description, 512)) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "user profile fields exceed their limits or email is invalid")
		return
	}
	updated, err := h.users.UpdateUser(r.Context(), profile, string(params.IfMatch))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
		return
	}
	if errors.Is(err, repository.ErrLastActiveAdmin) {
		writeHostedProblem(w, http.StatusConflict, "last_admin", "the last active administrator cannot be disabled or demoted")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "update user failed")
		return
	}
	h.recordUserManagementAudit(r, updated, "user.update", http.StatusOK)
	writeNativeMavenJSON(w, http.StatusOK, userResponse(updated))
}

func (h generatedRepositoryAPIAdapter) DeleteUser(w http.ResponseWriter, r *http.Request, userID string) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	current, err := h.users.GetUser(r.Context(), userID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get user failed")
		return
	}
	if err := h.users.DeleteUser(r.Context(), userID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
			return
		}
		if errors.Is(err, repository.ErrLastActiveAdmin) {
			writeHostedProblem(w, http.StatusConflict, "last_admin", "the last active administrator cannot be deleted")
			return
		}
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "delete user failed")
		return
	}
	h.recordUserManagementAudit(r, current, "user.delete", http.StatusNoContent)
	w.WriteHeader(http.StatusNoContent)
}

func (h generatedRepositoryAPIAdapter) ResetUserPassword(w http.ResponseWriter, r *http.Request, userID string, params adminopenapi.ResetUserPasswordParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	var request adminopenapi.ResetUserPassword
	if err := decoder.Decode(&request); err != nil || !validLocalPassword(request.Password) {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "password must contain at least 8 characters and at most 72 bytes")
		return
	}
	_, err := h.users.GetUser(r.Context(), userID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get user failed")
		return
	}
	hash, err := authorization.HashPassword(request.Password)
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "hash password failed")
		return
	}
	mustChange := true
	if request.MustChangePassword != nil {
		mustChange = *request.MustChangePassword
	}
	updated, err := h.users.UpdateUserPassword(r.Context(), userID, hash, string(params.IfMatch), mustChange)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "reset user password failed")
		return
	}
	h.recordUserManagementAudit(r, updated, "user.password.reset", http.StatusOK)
	writeNativeMavenJSON(w, http.StatusOK, userResponse(updated))
}

func (h generatedRepositoryAPIAdapter) RevokeUserSessions(w http.ResponseWriter, r *http.Request, userID string, params adminopenapi.RevokeUserSessionsParams) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	user, err := h.users.RevokeUserSessions(r.Context(), userID, string(params.IfMatch))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if errors.Is(err, repository.ErrVersionConflict) {
		writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "revoke user sessions failed")
		return
	}
	h.recordUserManagementAudit(r, user, "user.sessions.revoke", http.StatusOK)
	writeNativeMavenJSON(w, http.StatusOK, userResponse(user))
}

func (h generatedRepositoryAPIAdapter) ListUserSessions(w http.ResponseWriter, r *http.Request, userID openapi_types.UUID, params adminopenapi.ListUserSessionsParams) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	includeInactive := params.IncludeInactive != nil && *params.IncludeInactive
	items, err := h.users.ListUserSessions(r.Context(), userID.String(), includeInactive)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list user sessions failed")
		return
	}
	response := make([]adminopenapi.UserSession, 0, len(items))
	for _, session := range items {
		response = append(response, userSessionResponse(session, principal.UserID == session.UserID && principal.SessionID == session.ID))
	}
	if user, getErr := h.users.GetUser(r.Context(), userID.String()); getErr == nil {
		h.recordUserManagementAudit(r, user, "user.session.list", http.StatusOK)
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.UserSessionList{Items: response})
}

func (h generatedRepositoryAPIAdapter) RevokeUserSession(w http.ResponseWriter, r *http.Request, userID, sessionID openapi_types.UUID) {
	principal, ok := h.authorize(w, r)
	if !ok {
		return
	}
	session, err := h.users.RevokeUserSession(r.Context(), userID.String(), sessionID.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user session not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "revoke user session failed")
		return
	}
	if user, getErr := h.users.GetUser(r.Context(), userID.String()); getErr == nil {
		h.recordUserManagementAudit(r, user, "user.session.revoke", http.StatusOK)
	}
	writeNativeMavenJSON(w, http.StatusOK, userSessionResponse(session, principal.UserID == session.UserID && principal.SessionID == session.ID))
}

func (h generatedRepositoryAPIAdapter) ListUserIdentities(w http.ResponseWriter, r *http.Request, userID string) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	identities, err := h.users.ListUserIdentities(r.Context(), userID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "list user identities failed")
		return
	}
	items := make([]adminopenapi.UserIdentity, 0, len(identities))
	for _, identity := range identities {
		items = append(items, userIdentityResponse(identity))
	}
	writeNativeMavenJSON(w, http.StatusOK, adminopenapi.UserIdentityList{Items: items})
}

func (h generatedRepositoryAPIAdapter) CreateUserIdentity(w http.ResponseWriter, r *http.Request, userID string) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	var request adminopenapi.CreateUserIdentity
	if err := decoder.Decode(&request); err != nil || strings.TrimSpace(request.Issuer) == "" || strings.TrimSpace(request.Subject) == "" || len([]rune(request.Subject)) > 512 {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "issuer and subject are required")
		return
	}
	issuer := normalizeOIDCIssuerForAPI(request.Issuer)
	if h.oidcRuntime == nil {
		writeHostedProblem(w, http.StatusServiceUnavailable, "not_supported", "runtime OIDC settings are unavailable")
		return
	}
	settings, err := h.oidcRuntime.Settings(r.Context())
	if err != nil || normalizeOIDCIssuerForAPI(settings.Issuer) != issuer {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "identity issuer must match the configured OIDC issuer")
		return
	}
	identity, err := h.users.CreateUserIdentity(r.Context(), repository.UserIdentity{
		ID: uuid.NewString(), UserID: userID, Kind: repository.UserIdentityOIDC,
		Issuer: issuer, Subject: strings.TrimSpace(request.Subject),
	})
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if errors.Is(err, repository.ErrIdentityExists) {
		writeHostedProblem(w, http.StatusConflict, "identity_exists", "identity is already linked")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create user identity failed")
		return
	}
	h.recordUserIdentityAudit(r, identity, "user.identity.link", http.StatusCreated)
	writeNativeMavenJSON(w, http.StatusCreated, userIdentityResponse(identity))
}

func (h generatedRepositoryAPIAdapter) DeleteUserIdentity(w http.ResponseWriter, r *http.Request, userID, identityID string) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	if err := h.users.DeleteUserIdentity(r.Context(), userID, identityID); errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "user identity not found")
		return
	} else if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "delete user identity failed")
		return
	}
	if h.audit != nil {
		_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
			Actor: h.auditActor(r), Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(),
			Format: "management", Resource: "users/" + userID + "/identities/" + identityID,
			Operation: "user.identity.unlink", Status: http.StatusNoContent, CacheDisposition: "bypass",
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h generatedRepositoryAPIAdapter) recordUserIdentityAudit(r *http.Request, identity repository.UserIdentity, operation string, status int) {
	if h.audit == nil {
		return
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		Actor: h.auditActor(r), Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(),
		Format: "management", Resource: "users/" + identity.UserID + "/identities/" + identity.ID,
		Operation: operation, Status: status, CacheDisposition: "bypass",
	})
}

func (h generatedRepositoryAPIAdapter) auditActor(r *http.Request) string {
	if principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization")); ok {
		return principal.Actor
	}
	return "unknown"
}

func normalizeOIDCIssuerForAPI(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}

func (h generatedRepositoryAPIAdapter) recordUserManagementAudit(r *http.Request, user repository.User, operation string, status int) {
	if h.audit == nil {
		return
	}
	actor := "unknown"
	if principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization")); ok {
		actor = principal.Actor
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: actor, Outcome: repository.AuditResolved, OccurredAt: time.Now().UTC(), Format: "management", Resource: "users/" + user.ID, Operation: operation, Status: status, CacheDisposition: "bypass"})
}
