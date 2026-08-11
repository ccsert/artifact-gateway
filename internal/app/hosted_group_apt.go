package app

import (
	"errors"
	"net/http"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type v2GroupAPTHandler struct {
	native *nativeAPTHandler
}

func (h v2GroupAPTHandler) serve(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup) {
	if h.native == nil {
		http.NotFound(w, r)
		return
	}
	route, ok := parseAPTPath(r.URL.EscapedPath())
	if !ok || route.repository != group.Name {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	principal, authenticated := h.native.protocolPrincipal(r)
	if !authenticated {
		if group.AnonymousRead && anonymousReadMethod(r.Method) && anonymousAccessAllowed(r.Context(), resolver.groups) {
			principal = anonymousPrincipal()
		} else {
			h.native.challenge(w, http.StatusUnauthorized, "authentication required")
			return
		}
	}
	members, err := resolver.resolveMembers(r.Context(), group)
	if err != nil {
		http.Error(w, "unable to resolve group members", http.StatusServiceUnavailable)
		return
	}
	if isAnonymous(principal) {
		members = anonymousHostedGroupMembers(group, members)
	}
	members, denied := resolver.authorizeRepositoryMembers(r.Context(), principal, group.Name, repository.FormatAPT, route.path, members)
	if denied && len(members) == 0 {
		h.native.challenge(w, http.StatusForbidden, "repository permission required")
		return
	}
	var lastErr error
	for _, member := range members {
		repo, getErr := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if getErr != nil || repo.Type != repository.RepositoryTypeProxy {
			continue
		}
		asset, disposition, loadErr := h.native.loadAsset(r, repo, route.path)
		if errors.Is(loadErr, repository.ErrNotFound) {
			continue
		}
		if loadErr != nil {
			lastErr = loadErr
			continue
		}
		if disposition == "stale" {
			w.Header().Set("Warning", `110 Artifact-Gateway "Response is stale"`)
		}
		status, _, writeErr := h.native.writeAsset(w, r, asset)
		if writeErr != nil {
			return
		}
		resolver.auditResolution(r.Context(), group, repository.FormatAPT, route.path, strings.ToLower(r.Method), principal.Actor, repository.AuditResolved, status)
		return
	}
	if lastErr != nil {
		http.Error(w, "APT upstream unavailable", http.StatusBadGateway)
		resolver.auditResolution(r.Context(), group, repository.FormatAPT, route.path, strings.ToLower(r.Method), principal.Actor, repository.AuditUpstreamError, http.StatusBadGateway)
		return
	}
	http.NotFound(w, r)
	resolver.auditResolution(r.Context(), group, repository.FormatAPT, route.path, strings.ToLower(r.Method), principal.Actor, repository.AuditNotFound, http.StatusNotFound)
}
