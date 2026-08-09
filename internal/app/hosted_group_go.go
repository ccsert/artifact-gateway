package app

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"golang.org/x/mod/semver"
)

type v2GroupGoHandler struct {
	native *nativeGoHandler
}

func (h v2GroupGoHandler) serve(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup) {
	if h.native == nil {
		http.NotFound(w, r)
		return
	}
	route, ok := parseGoProxyPath(r.URL.EscapedPath())
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
	members, denied := resolver.authorizeRepositoryMembers(r.Context(), principal, group.Name, repository.FormatGo, route.module, members)
	if denied && len(members) == 0 {
		h.native.challenge(w, http.StatusForbidden, "repository permission required")
		return
	}
	if len(members) == 0 {
		h.native.challenge(w, http.StatusUnauthorized, "authentication required")
		return
	}
	switch route.kind {
	case "list":
		h.serveList(w, r, resolver, group, members, route.module, principal.Actor)
	case "latest":
		h.serveLatest(w, r, resolver, group, members, route.module, principal.Actor)
	case "info", "mod", "zip":
		h.serveAsset(w, r, resolver, group, members, route, principal.Actor)
	default:
		http.NotFound(w, r)
	}
}

func (h v2GroupGoHandler) versions(r *http.Request, resolver v2GroupResolver, members []repository.Member, modulePath string) ([]repository.GoModuleVersion, bool) {
	seen := make(map[string]repository.GoModuleVersion)
	stale := false
	for _, member := range members {
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil || repo.Type != repository.RepositoryTypeProxy {
			continue
		}
		versions, disposition, err := h.native.resolveList(r, repo, modulePath)
		if err != nil {
			continue
		}
		stale = stale || disposition == "stale"
		for _, version := range versions {
			if _, exists := seen[version.Version]; !exists {
				seen[version.Version] = version
			}
		}
	}
	versions := make([]repository.GoModuleVersion, 0, len(seen))
	for _, version := range seen {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(i, j int) bool { return semver.Compare(versions[i].Version, versions[j].Version) < 0 })
	return versions, stale
}

func (h v2GroupGoHandler) serveList(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, modulePath, actor string) {
	versions, stale := h.versions(r, resolver, members, modulePath)
	if len(versions) == 0 {
		http.NotFound(w, r)
		resolver.auditResolution(r.Context(), group, repository.FormatGo, modulePath, strings.ToLower(r.Method), actor, repository.AuditNotFound, http.StatusNotFound)
		return
	}
	var body strings.Builder
	for _, version := range versions {
		body.WriteString(version.Version)
		body.WriteByte('\n')
	}
	if stale {
		w.Header().Set("Warning", `110 Artifact-Gateway "Response is stale"`)
	}
	h.native.writeBytes(w, r, []byte(body.String()), "text/plain; charset=utf-8", "")
	resolver.auditResolution(r.Context(), group, repository.FormatGo, modulePath, strings.ToLower(r.Method), actor, repository.AuditResolved, http.StatusOK)
}

func (h v2GroupGoHandler) serveLatest(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, modulePath, actor string) {
	versions, _ := h.versions(r, resolver, members, modulePath)
	if len(versions) == 0 {
		http.NotFound(w, r)
		return
	}
	sort.Slice(versions, func(i, j int) bool { return semver.Compare(versions[i].Version, versions[j].Version) > 0 })
	h.serveAsset(w, r, resolver, group, members, goRoute{module: modulePath, version: versions[0].Version, kind: "info"}, actor)
}

func (h v2GroupGoHandler) serveAsset(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, route goRoute, actor string) {
	var lastErr error
	for _, member := range members {
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil || repo.Type != repository.RepositoryTypeProxy {
			continue
		}
		asset, _, err := h.native.loadAsset(r, repo, route)
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			lastErr = err
			continue
		}
		_, status, err := h.native.writeAsset(w, r, asset, route.kind)
		if err != nil {
			return
		}
		resolver.auditResolution(r.Context(), group, repository.FormatGo, route.module+"@"+route.version, strings.ToLower(r.Method), actor, repository.AuditResolved, status)
		return
	}
	if lastErr != nil {
		http.Error(w, "Go module upstream unavailable", http.StatusBadGateway)
		resolver.auditResolution(r.Context(), group, repository.FormatGo, route.module+"@"+route.version, strings.ToLower(r.Method), actor, repository.AuditUpstreamError, http.StatusBadGateway)
		return
	}
	http.NotFound(w, r)
	resolver.auditResolution(r.Context(), group, repository.FormatGo, route.module+"@"+route.version, strings.ToLower(r.Method), actor, repository.AuditNotFound, http.StatusNotFound)
}
