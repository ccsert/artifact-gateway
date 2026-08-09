package app

import (
	"errors"
	"net/http"
	"strings"

	npmprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/npm"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// v2GroupNPMHandler exposes multiple npm Hosted and Proxy repositories as one
// registry. Packuments are merged by member priority; a version or dist-tag
// already supplied by a higher-priority member is never overwritten.
type v2GroupNPMHandler struct {
	native *nativeNPMHandler
}

func (h v2GroupNPMHandler) serve(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup) {
	if h.native == nil {
		http.NotFound(w, r)
		return
	}
	route, ok := npmprotocol.ParsePath(r.URL.EscapedPath())
	if !ok || route.Repository != group.Name {
		http.NotFound(w, r)
		return
	}
	if h.native.metrics != nil {
		h.native.metrics.recordNPMRequest(r.Method)
	}

	principal, authenticated := h.native.protocolPrincipal(r)
	if !authenticated {
		anonymousMethod := r.Method
		if route.Kind == npmprotocol.RouteAuditBulk || route.Kind == npmprotocol.RouteAuditQuick {
			anonymousMethod = http.MethodGet
		}
		if group.AnonymousRead && anonymousReadMethod(anonymousMethod) && anonymousAccessAllowed(r.Context(), resolver.groups) {
			principal = anonymousPrincipal()
		} else {
			h.native.challenge(w, http.StatusUnauthorized, "authentication required")
			return
		}
	}

	members, err := resolver.resolveMembers(r.Context(), group)
	if err != nil {
		h.native.writeError(w, http.StatusServiceUnavailable, "unable to resolve group members")
		return
	}
	if isAnonymous(principal) {
		members = anonymousHostedGroupMembers(group, members)
	}
	members, denied := resolver.authorizeRepositoryMembers(r.Context(), principal, group.Name, repository.FormatNPM, route.Package, members)
	if denied && len(members) == 0 {
		h.native.challenge(w, http.StatusForbidden, "repository permission required")
		return
	}
	if len(members) == 0 {
		h.native.challenge(w, http.StatusUnauthorized, "authentication required")
		return
	}

	switch route.Kind {
	case npmprotocol.RoutePing:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.native.writeJSON(w, r, http.StatusOK, map[string]any{"ok": true})
	case npmprotocol.RouteAuditBulk:
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.native.writeJSON(w, r, http.StatusOK, map[string]any{})
	case npmprotocol.RouteAuditQuick:
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.native.writeJSON(w, r, http.StatusOK, emptyNPMAuditReport())
	case npmprotocol.RoutePackage:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.packument(w, r, resolver, group, members, route.Package, principal.Actor)
	case npmprotocol.RouteTarball:
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		h.tarball(w, r, resolver, group, members, route.Package, route.Tarball, principal.Actor)
	}
}

func (h v2GroupNPMHandler) packument(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, packageName, actor string) {
	merged := repository.NPMPackage{Name: packageName, DistTags: make(map[string]string)}
	seenVersions := make(map[string]bool)
	var firstError *npmProxyPackageError
	stale := false

	for _, member := range members {
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			if firstError == nil && !errors.Is(err, repository.ErrNotFound) {
				firstError = &npmProxyPackageError{Status: http.StatusServiceUnavailable, Message: "package metadata unavailable"}
			}
			continue
		}
		var pkg repository.NPMPackage
		if repo.Type == repository.RepositoryTypeProxy {
			resolved, resolveErr := h.native.resolveProxyPackageWithAudit(r, repo, packageName, actor, npmAuditTarget{GroupName: group.Name, Repository: group.Name, MemberName: repo.Name})
			if resolveErr != nil {
				var responseError *npmProxyPackageError
				if errors.As(resolveErr, &responseError) && responseError.Status != http.StatusNotFound && firstError == nil {
					firstError = responseError
				}
				continue
			}
			pkg = resolved.Package
			stale = stale || resolved.Stale
		} else {
			pkg, err = h.native.store.GetNPMPackage(r.Context(), repo.ID, packageName)
			if err != nil {
				if !errors.Is(err, repository.ErrNotFound) && firstError == nil {
					firstError = &npmProxyPackageError{Status: http.StatusServiceUnavailable, Message: "package metadata unavailable"}
				}
				continue
			}
		}
		mergeNPMGroupPackage(&merged, pkg, seenVersions)
	}

	if len(merged.Versions) == 0 {
		if firstError != nil {
			resolver.auditResolution(r.Context(), group, repository.FormatNPM, packageName, strings.ToLower(r.Method), actor, repository.AuditUpstreamError, firstError.Status)
			h.native.writeError(w, firstError.Status, firstError.Message)
		} else {
			resolver.auditResolution(r.Context(), group, repository.FormatNPM, packageName, strings.ToLower(r.Method), actor, repository.AuditNotFound, http.StatusNotFound)
			h.native.writeError(w, http.StatusNotFound, "package not found")
		}
		return
	}
	if stale {
		w.Header().Set("Warning", `110 Artifact-Gateway "Response is stale"`)
	}
	groupRepo := repository.HostedRepository{ID: group.ID, Name: group.Name, Format: repository.FormatNPM, Type: repository.RepositoryTypeHosted}
	h.native.writePackument(w, r, groupRepo, group.Name, merged, actor, "bypass")
}

func mergeNPMGroupPackage(merged *repository.NPMPackage, incoming repository.NPMPackage, seenVersions map[string]bool) {
	if merged.CreatedAt.IsZero() || !incoming.CreatedAt.IsZero() && incoming.CreatedAt.Before(merged.CreatedAt) {
		merged.CreatedAt = incoming.CreatedAt
	}
	if incoming.UpdatedAt.After(merged.UpdatedAt) {
		merged.UpdatedAt = incoming.UpdatedAt
	}
	for _, version := range incoming.Versions {
		if seenVersions[version.Version] {
			continue
		}
		seenVersions[version.Version] = true
		merged.Versions = append(merged.Versions, version)
	}
	for tag, version := range incoming.DistTags {
		if _, exists := merged.DistTags[tag]; !exists {
			merged.DistTags[tag] = version
		}
	}
}

func (h v2GroupNPMHandler) tarball(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, packageName, tarballName, actor string) {
	for _, member := range members {
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			continue
		}
		_, err = h.native.store.GetNPMVersionByTarball(r.Context(), repo.ID, packageName, tarballName)
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			h.native.writeError(w, http.StatusServiceUnavailable, "package tarball unavailable")
			return
		}
		auditTarget := npmAuditTarget{GroupName: group.Name, Repository: group.Name, MemberName: repo.Name}
		if repo.Type == repository.RepositoryTypeProxy {
			h.native.proxyTarballWithAudit(w, r, repo, packageName, tarballName, actor, auditTarget)
		} else {
			h.native.tarballWithAudit(w, r, repo, packageName, tarballName, actor, "bypass", auditTarget)
		}
		return
	}
	resolver.auditResolution(r.Context(), group, repository.FormatNPM, packageName+"/-/"+tarballName, strings.ToLower(r.Method), actor, repository.AuditNotFound, http.StatusNotFound)
	h.native.writeError(w, http.StatusNotFound, "package tarball not found")
}
