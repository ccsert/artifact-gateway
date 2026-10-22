package app

import (
	"errors"
	"net/http"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

type artifactManagementOptions struct {
	Scope                managementScope
	Repositories         artifactRepositoryCatalog
	Groups               artifactGroupReader
	Grants               artifactGrantReader
	MavenPublisher       mavenManagementOperations
	ConanPublisher       http.Handler
	ConanSessions        artifactConanSessionReader
	Maven                artifactMavenReader
	Raw                  artifactRawReader
	NPM                  artifactNPMReader
	PyPI                 artifactPyPIReader
	APT                  artifactAPTReader
	OCI                  artifactOCIReader
	Conan                artifactConanStore
	Cargo                artifactCargoReader
	CargoProxy           artifactCargoProxyReader
	Intelligence         repository.ArtifactIntelligenceStore
	SearchProjection     repository.ArtifactSearchStore
	Identities           repository.ArtifactIdentityStore
	Usage                artifactUsageReader
	Browse               repository.ArtifactBrowseStore
	ProxyOperations      artifactProxyOperations
	ProxyDirectory       proxyDirectoryStore
	MavenProxyOperations artifactMavenProxyOperations
	MavenCache           *MavenCache
}

// artifactManagementHandler owns management discovery and publishing. Its
// query ports and native operation delegates are explicit and independent.
type artifactManagementHandler struct {
	managementScope
	store            artifactRepositoryCatalog
	groups           artifactGroupReader
	grants           artifactGrantReader
	sessions         mavenManagementOperations
	conanPublish     http.Handler
	conanSessions    artifactConanSessionReader
	maven            artifactMavenReader
	raw              artifactRawReader
	npm              artifactNPMReader
	pypi             artifactPyPIReader
	apt              artifactAPTReader
	oci              artifactOCIReader
	conan            artifactConanStore
	cargo            artifactCargoReader
	cargoProxy       artifactCargoProxyReader
	intelligence     repository.ArtifactIntelligenceStore
	searchProjection repository.ArtifactSearchStore
	identities       repository.ArtifactIdentityStore
	usage            artifactUsageReader
	browse           repository.ArtifactBrowseStore
	proxyCache       artifactProxyOperations
	proxyDirectory   proxyDirectoryStore
	mavenProxy       artifactMavenProxyOperations
	mavenCache       *MavenCache
}

func newArtifactManagementHandler(options artifactManagementOptions) artifactManagementHandler {
	return artifactManagementHandler{
		managementScope:  options.Scope,
		store:            options.Repositories,
		groups:           options.Groups,
		grants:           options.Grants,
		sessions:         options.MavenPublisher,
		conanPublish:     options.ConanPublisher,
		conanSessions:    options.ConanSessions,
		maven:            options.Maven,
		raw:              options.Raw,
		npm:              options.NPM,
		pypi:             options.PyPI,
		apt:              options.APT,
		oci:              options.OCI,
		conan:            options.Conan,
		cargo:            options.Cargo,
		cargoProxy:       options.CargoProxy,
		intelligence:     options.Intelligence,
		searchProjection: options.SearchProjection,
		identities:       options.Identities,
		usage:            options.Usage,
		browse:           options.Browse,
		proxyCache:       options.ProxyOperations,
		proxyDirectory:   options.ProxyDirectory,
		mavenProxy:       options.MavenProxyOperations,
		mavenCache:       options.MavenCache,
	}
}

func (h artifactManagementHandler) authorizeAnonymousGroupBrowse(w http.ResponseWriter, r *http.Request, group repository.HostedGroup) bool {
	if !repository.IsSupportedFormat(group.Format) {
		writeHostedProblem(w, 404, "not_found", "group not found")
		return false
	}
	if principal, authenticated := h.authenticator.AuthenticateContext(r.Context(), r.Header.Get("Authorization")); authenticated {
		if code, message, blocked := accountStateProblem(principal.AccountStateReason()); blocked {
			writeHostedProblem(w, http.StatusForbidden, code, message)
			return false
		}
	}
	if !anonymousHostedGroupReadAllowed(r.Context(), h.anonymousPolicy, h.store, group, r.Method) {
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "group anonymous read is not enabled")
		return false
	}
	return true
}

func (h artifactManagementHandler) withSessionScope(w http.ResponseWriter, r *http.Request, sessionID string, operation RepositoryOperation, handler func(Principal, repository.HostedRepository)) {
	principal, ok := h.authenticateArtifactCaller(w, r)
	if !ok {
		return
	}
	if h.maven == nil {
		h.unavailableSessionLookup(w, principal)
		return
	}
	session, err := h.maven.GetMavenPublishSession(r.Context(), sessionID)
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "publish session not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get publish session failed")
		return
	}
	h.withRepositoryScopeForPrincipal(w, r, principal, session.RepositoryID, operation, handler)
}
