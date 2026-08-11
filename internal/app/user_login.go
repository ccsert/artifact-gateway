package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// userLoginHandler exchanges local username/password credentials for a
// stateless session token. Unknown users, wrong passwords, and disabled
// accounts all return the same error to avoid user enumeration.
type userLoginAuditStore interface {
	RecordAudit(context.Context, repository.AuditRecord) error
}

func userLoginHandler(users repository.UserStore, audit userLoginAuditStore, authenticator Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.Username) == "" || body.Password == "" {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "username and password are required")
			return
		}
		body.Username = strings.TrimSpace(body.Username)
		now := time.Now().UTC()
		user, err := users.GetUserByName(r.Context(), body.Username)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}
		locked := user.LockedUntil != nil && now.Before(*user.LockedUntil)
		if errors.Is(err, repository.ErrNotFound) || user.State != repository.UserActive || locked {
			recordUserLoginAudit(r, audit, body.Username, repository.AuditAccessDenied, http.StatusUnauthorized, "invalid credentials")
			writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "invalid credentials")
			return
		}
		if !authorization.VerifyPassword(user.SecretHash, body.Password) {
			maxAttempts, lockout := localAuthPolicy(authenticator)
			_, _ = users.RecordUserLoginFailure(r.Context(), user.ID, now, maxAttempts, lockout)
			recordUserLoginAudit(r, audit, body.Username, repository.AuditAccessDenied, http.StatusUnauthorized, "invalid credentials")
			writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "invalid credentials")
			return
		}
		user, err = users.RecordUserLoginSuccess(r.Context(), user.ID, now)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "login failed")
			return
		}
		sessionID := newUserSessionID()
		if authenticator.UserSessions != nil {
			if _, err := authenticator.UserSessions.CreateUserSession(r.Context(), repository.UserSession{
				ID: sessionID, UserID: user.ID, Kind: repository.UserSessionLocal,
				CreatedAt: now, ExpiresAt: now.Add(authorization.UserSessionLifetime),
				IPAddress: requestIPAddress(r), UserAgent: boundedUserAgent(r.UserAgent()),
			}); err != nil {
				writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "create user session failed")
				return
			}
		}
		token := authenticator.IssueUserSessionWithID(user.ID, sessionID, user.SessionVersion)
		recordUserLoginAudit(r, audit, user.Name, repository.AuditResolved, http.StatusOK, "authenticated")
		writeNativeMavenJSON(w, http.StatusOK, map[string]any{"token": token, "username": user.Name, "displayName": user.DisplayName, "role": user.Role, "mustChangePassword": user.MustChangePassword})
	}
}

func newUserSessionID() string { return uuid.NewString() }

func requestIPAddress(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		value = value[:comma]
	}
	if value == "" {
		value = strings.TrimSpace(r.RemoteAddr)
		if host, _, err := net.SplitHostPort(value); err == nil {
			value = host
		}
	}
	return boundedString(value, 64)
}

func boundedUserAgent(value string) string { return boundedString(strings.TrimSpace(value), 512) }

func boundedString(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func localAuthPolicy(authenticator Authenticator) (int, time.Duration) {
	maxAttempts := authenticator.LocalAuthMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	lockout := authenticator.LocalAuthLockout
	if lockout <= 0 {
		lockout = 15 * time.Minute
	}
	return maxAttempts, lockout
}

func recordUserLoginAudit(r *http.Request, audit userLoginAuditStore, username string, outcome repository.AuditOutcome, status int, reason string) {
	recordUserAudit(r, audit, username, "auth/login", "user.login", outcome, status, reason)
}

func recordUserAudit(r *http.Request, audit userLoginAuditStore, username, resource, operation string, outcome repository.AuditOutcome, status int, reason string) {
	if audit == nil {
		return
	}
	_ = audit.RecordAudit(r.Context(), repository.AuditRecord{
		Actor: "user:" + strings.TrimSpace(username), Outcome: outcome,
		OccurredAt: time.Now().UTC(), Format: "management", Resource: resource,
		Operation: operation, Status: status, CacheDisposition: "bypass",
		AuthorizationSource: "local_password", AuthorizationReason: reason,
	})
}

func userChangePasswordHandler(users repository.UserStore, audit userLoginAuditStore, authenticator Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := authenticator.Authenticate(r.Header.Get("Authorization"))
		if !ok || principal.AuthenticationKind != authorization.AuthenticationLocalSession || !strings.HasPrefix(principal.Actor, "user:") {
			writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "a local user session is required")
			return
		}
		var body struct {
			CurrentPassword string `json:"currentPassword"`
			NewPassword     string `json:"newPassword"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || len(body.CurrentPassword) < 1 || !validLocalPassword(body.NewPassword) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "currentPassword is required and newPassword must contain at least 8 characters and at most 72 bytes")
			return
		}
		username := strings.TrimPrefix(principal.Actor, "user:")
		user, err := users.GetUserByName(r.Context(), username)
		if errors.Is(err, repository.ErrNotFound) || err == nil && !authorization.VerifyPassword(user.SecretHash, body.CurrentPassword) {
			recordUserAudit(r, audit, username, "auth/change-password", "user.password.change", repository.AuditAccessDenied, http.StatusUnauthorized, "current password did not match")
			writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "current password is invalid")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "load user failed")
			return
		}
		if body.NewPassword == body.CurrentPassword {
			recordUserAudit(r, audit, username, "auth/change-password", "user.password.change", repository.AuditAccessDenied, http.StatusBadRequest, "new password matched current password")
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "new password must differ from the current password")
			return
		}
		hash, err := authorization.HashPassword(body.NewPassword)
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "hash password failed")
			return
		}
		updated, err := users.UpdateUserPassword(r.Context(), user.ID, hash, user.Version, false)
		if errors.Is(err, repository.ErrVersionConflict) {
			writeHostedProblem(w, http.StatusPreconditionFailed, "version_conflict", "user changed concurrently; retry login")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "change password failed")
			return
		}
		recordUserAudit(r, audit, updated.Name, "auth/change-password", "user.password.change", repository.AuditResolved, http.StatusNoContent, "password changed")
		w.WriteHeader(http.StatusNoContent)
	}
}
