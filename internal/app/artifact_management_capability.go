package app

import (
	"errors"
	"net/http"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

var errArtifactCapabilityUnavailable = errors.New("artifact management capability is not configured")

func requireArtifactCapability(w http.ResponseWriter, available bool) bool {
	if !available {
		writeHostedProblem(w, http.StatusNotImplemented, "not_supported", errArtifactCapabilityUnavailable.Error())
	}
	return available
}

func writeArtifactQueryFailure(w http.ResponseWriter, err error, message string) {
	if errors.Is(err, errArtifactCapabilityUnavailable) {
		requireArtifactCapability(w, false)
		return
	}
	writeHostedProblem(w, http.StatusInternalServerError, "internal_error", message)
}

// Native format search and projection search have different dependencies. A
// missing port is configuration evidence, never an empty artifact inventory.
func (h artifactManagementHandler) nativeSearchConfigured(repo repository.HostedRepository) bool {
	available := map[repository.Format]bool{
		repository.FormatOCI: h.oci != nil, repository.FormatMaven: h.maven != nil,
		repository.FormatConan: h.conan != nil, repository.FormatRaw: h.raw != nil,
		repository.FormatNPM: h.npm != nil, repository.FormatPyPI: h.pypi != nil,
		repository.FormatCargo: h.cargo != nil, repository.FormatAPT: h.apt != nil && repo.Type == repository.RepositoryTypeProxy,
	}
	if repo.Format == repository.FormatCargo && repo.Type == repository.RepositoryTypeProxy {
		return h.cargoProxy != nil
	}
	return available[repo.Format]
}

func (h artifactManagementHandler) repositorySearchConfigured(repo repository.HostedRepository) bool {
	if repo.Format == repository.FormatGo || repo.Format == repository.FormatAPT && repo.Type != repository.RepositoryTypeProxy {
		return h.searchProjection != nil
	}
	return h.nativeSearchConfigured(repo)
}

func (h artifactManagementHandler) projectedSearchConfigured(repo repository.HostedRepository, query repository.ArtifactSearchQuery) bool {
	if repo.Format == repository.FormatCargo {
		return h.nativeSearchConfigured(repo)
	}
	if h.searchProjection != nil {
		return true
	}
	if query.Mode != repository.ArtifactSearchByCoordinate || repo.Format == repository.FormatAPT || repo.Format == repository.FormatGo {
		return false
	}
	return h.nativeSearchConfigured(repo)
}

func (h artifactManagementHandler) authenticateArtifactCaller(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return Principal{}, false
	}
	if code, message, blocked := accountStateProblem(principal.AccountStateReason()); blocked {
		writeHostedProblem(w, http.StatusForbidden, code, message)
		return Principal{}, false
	}
	return principal, true
}

// Without session metadata only the platform administrator can establish that
// a global capability is unavailable; other callers retain an opaque 404.
func (h artifactManagementHandler) unavailableSessionLookup(w http.ResponseWriter, principal Principal) {
	if !principal.Admin {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	requireArtifactCapability(w, false)
}

func (h artifactManagementHandler) unavailableConanPublisher(w http.ResponseWriter, r *http.Request, sessionID string, operation RepositoryOperation) {
	principal, ok := h.authenticateArtifactCaller(w, r)
	if !ok {
		return
	}
	if h.conanSessions == nil {
		h.unavailableSessionLookup(w, principal)
		return
	}
	session, err := h.conanSessions.GetConanPublishSession(r.Context(), sessionID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Conan publish session not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "load Conan publish session failed")
		return
	}
	repo, err := h.repositories.GetHostedRepository(r.Context(), session.RepositoryID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if decision := h.authorizer.AuthorizeResource(r.Context(), principal, repo, operation, conanSessionResource(session)); !decision.Allowed {
		h.writeRepositoryDenial(w, r, principal, repo, operation, decision)
		return
	}
	requireArtifactCapability(w, false)
}
