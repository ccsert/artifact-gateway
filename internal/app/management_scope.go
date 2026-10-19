package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

type managementRepositoryReader interface {
	GetHostedRepository(context.Context, string) (repository.HostedRepository, error)
}

type managementAnonymousPolicyReader interface {
	GetAnonymousAccessPolicy(context.Context) (repository.AnonymousAccessPolicy, error)
}

type managementAuditRecorder interface {
	RecordAudit(context.Context, repository.AuditRecord) error
}

// managementScope shares authorization policy, not domain persistence. Each
// domain receives its own store ports separately.
type managementScope struct {
	repositories    managementRepositoryReader
	anonymousPolicy managementAnonymousPolicyReader
	authenticator   Authenticator
	authorizer      RepositoryAuthorizer
	audit           managementAuditRecorder
	metrics         *Metrics
}

func newManagementScope(repositories managementRepositoryReader, anonymousPolicy managementAnonymousPolicyReader, grants repository.RepositoryGrantStore, audit managementAuditRecorder, authenticator Authenticator, metrics *Metrics) managementScope {
	return managementScope{repositories: repositories, anonymousPolicy: anonymousPolicy, authenticator: authenticator, authorizer: RepositoryAuthorizer{Grants: grants, Legacy: authenticator}, audit: audit, metrics: metrics}
}

func (h managementScope) authorize(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return Principal{}, false
	}
	if !h.authorizePlatformAdministrator(w, principal) {
		return Principal{}, false
	}
	return principal, true
}

// authorizePlatformAdministrator answers a principal-wide block or a missing
// platform administration, so a caller that authenticated for a narrower reason
// can be checked without looking up its credential again.
func (h managementScope) authorizePlatformAdministrator(w http.ResponseWriter, principal Principal) bool {
	if principal.MustChangePassword {
		writeHostedProblem(w, http.StatusForbidden, "password_change_required", "password change is required")
		return false
	}
	if !principal.Admin {
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "administrator permission is required")
		return false
	}
	return true
}

func (h managementScope) authenticate(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.authenticator.AuthenticateContext(r.Context(), r.Header.Get("Authorization"))
	if !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return Principal{}, false
	}
	return principal, true
}

// writeRepositoryDenial answers a denied repository authorization decision. A
// principal-wide block reports its own problem code so a pending or
// password-change account is distinguishable from a plain scope denial; every
// repository entry point must route denials through here to stay in lockstep.
func (h managementScope) writeRepositoryDenial(w http.ResponseWriter, r *http.Request, principal Principal, repo repository.HostedRepository, operation RepositoryOperation, decision AuthorizationDecision) {
	h.recordAuthorizationDenial(r, principal, repo, operation, decision)
	if code, message, blocked := accountStateProblem(decision.Reason); blocked {
		writeHostedProblem(w, http.StatusForbidden, code, message)
		return
	}
	writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository scope is required")
}

func (h managementScope) withRepositoryScope(w http.ResponseWriter, r *http.Request, repositoryID string, operation RepositoryOperation, handler func(Principal, repository.HostedRepository)) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	repo, err := h.repositories.GetHostedRepository(r.Context(), repositoryID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, operation); !decision.Allowed {
		h.writeRepositoryDenial(w, r, principal, repo, operation, decision)
		return
	}
	handler(principal, repo)
}

func (h managementScope) withRepositoryBrowseScope(w http.ResponseWriter, r *http.Request, repositoryID string, handler func(Principal, repository.HostedRepository)) {
	principal, authenticated := h.authenticator.AuthenticateContext(r.Context(), r.Header.Get("Authorization"))
	repo, err := h.repositories.GetHostedRepository(r.Context(), repositoryID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if !authenticated {
		if anonymousHostedRepositoryReadAllowed(r.Context(), h.anonymousPolicy, repo, r.Method) {
			handler(anonymousPrincipal(), repo)
			return
		}
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, RepositoryRead); !decision.Allowed {
		h.writeRepositoryDenial(w, r, principal, repo, RepositoryRead, decision)
		return
	}
	handler(principal, repo)
}

func (h managementScope) withRepositoryScopeForPrincipal(w http.ResponseWriter, r *http.Request, principal Principal, repositoryID string, operation RepositoryOperation, handler func(Principal, repository.HostedRepository)) {
	repo, err := h.repositories.GetHostedRepository(r.Context(), repositoryID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, operation); !decision.Allowed {
		h.writeRepositoryDenial(w, r, principal, repo, operation, decision)
		return
	}
	handler(principal, repo)
}

// accountStateProblem maps a principal-wide block reported by the repository
// authorizer to a problem code, so every repository-facing entry point answers
// a pending or password-change account identically instead of falling back to a
// generic access denial.
func accountStateProblem(reason string) (string, string, bool) {
	switch reason {
	case "authorization_pending":
		return "authorization_pending", "administrator approval is required", true
	case "password_change_required":
		return "password_change_required", "password change is required", true
	}
	return "", "", false
}

func (h managementScope) recordAuthorizationDenial(r *http.Request, principal Principal, repo repository.HostedRepository, operation RepositoryOperation, decision AuthorizationDecision) {
	if h.metrics != nil {
		h.metrics.recordRepositoryAuthorizationDenied("management", decision.Source, decision.Reason)
	}
	if h.audit == nil {
		return
	}
	_ = h.audit.RecordAudit(r.Context(), repository.AuditRecord{
		GroupName: repo.Name, Repository: repo.Name, Actor: principal.Actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "management", Resource: "repositories/" + repo.ID, Operation: string(operation), Status: http.StatusForbidden, CacheDisposition: "bypass",
		AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason,
	})
}
