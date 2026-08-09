package app

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type v2GroupPyPIHandler struct {
	native *nativePyPIHandler
}

func (h v2GroupPyPIHandler) serve(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup) {
	if h.native == nil {
		http.NotFound(w, r)
		return
	}
	route, ok := parsePyPIPath(r.URL.EscapedPath())
	if !ok || route.repository != group.Name || route.kind == "legacy" {
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
		h.native.writeError(w, http.StatusServiceUnavailable, "unable to resolve group members")
		return
	}
	if isAnonymous(principal) {
		members = anonymousHostedGroupMembers(group, members)
	}
	resource := route.project
	if resource == "" {
		resource = route.filename
	}
	members, denied := resolver.authorizeRepositoryMembers(r.Context(), principal, group.Name, repository.FormatPyPI, resource, members)
	if denied && len(members) == 0 {
		h.native.challenge(w, http.StatusForbidden, "repository permission required")
		return
	}
	if len(members) == 0 {
		h.native.challenge(w, http.StatusUnauthorized, "authentication required")
		return
	}
	switch route.kind {
	case "simple-root":
		h.simpleRoot(w, r, resolver, group, members, principal.Actor)
	case "simple-project":
		h.simpleProject(w, r, resolver, group, members, route.project, principal.Actor)
	case "package":
		h.download(w, r, resolver, group, members, route.filename, principal.Actor)
	default:
		http.NotFound(w, r)
	}
}

func (h v2GroupPyPIHandler) simpleRoot(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, actor string) {
	seen := make(map[string]repository.PyPIProjectSummary)
	for _, member := range members {
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			continue
		}
		projects, err := h.native.store.ListPyPIProjects(r.Context(), repo.ID, "", 10000, "")
		if err != nil {
			continue
		}
		for _, project := range projects {
			if _, ok := seen[project.Project]; !ok {
				seen[project.Project] = project
			}
		}
	}
	projects := make([]repository.PyPIProjectSummary, 0, len(seen))
	for _, project := range seen {
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Project < projects[j].Project })
	groupRepo := repository.HostedRepository{ID: group.ID, Name: group.Name, Format: repository.FormatPyPI}
	h.native.simpleRootFromProjects(w, r, groupRepo, projects)
	resolver.auditResolution(r.Context(), group, repository.FormatPyPI, "", strings.ToLower(r.Method), actor, repository.AuditResolved, http.StatusOK)
}

func (h v2GroupPyPIHandler) simpleProject(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, project, actor string) {
	merged := make([]repository.PyPIFile, 0)
	seen := make(map[string]bool)
	stale := false
	for _, member := range members {
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			continue
		}
		var files []repository.PyPIFile
		disposition := "bypass"
		target := pypiAuditTarget{GroupName: group.Name, Repository: group.Name, MemberName: repo.Name}
		if repo.Type == repository.RepositoryTypeProxy {
			var resolveErr error
			files, disposition, resolveErr = h.native.resolveProxyProject(r, repo, project)
			if resolveErr != nil {
				outcome := repository.AuditUpstreamError
				status := http.StatusBadGateway
				if errors.Is(resolveErr, repository.ErrNotFound) {
					outcome = repository.AuditNotFound
					status = http.StatusNotFound
				}
				h.native.recordAuditForTarget(r, repo, target, project, "project", actor, outcome, status, 0, disposition)
				continue
			}
			stale = stale || disposition == "stale"
		} else {
			files, err = h.native.store.ListPyPIProjectFiles(r.Context(), repo.ID, project)
			if err != nil {
				if !errors.Is(err, repository.ErrNotFound) {
					h.native.recordAuditForTarget(r, repo, target, project, "project", actor, repository.AuditStorageError, http.StatusServiceUnavailable, 0, disposition)
				}
				continue
			}
		}
		h.native.recordAuditForTarget(r, repo, target, project, "project", actor, repository.AuditResolved, http.StatusOK, 0, disposition)
		for _, file := range files {
			if seen[file.Filename] {
				continue
			}
			seen[file.Filename] = true
			merged = append(merged, file)
		}
	}
	if len(merged) == 0 {
		resolver.auditResolution(r.Context(), group, repository.FormatPyPI, project, strings.ToLower(r.Method), actor, repository.AuditNotFound, http.StatusNotFound)
		http.NotFound(w, r)
		return
	}
	if stale {
		w.Header().Set("Warning", `110 Artifact-Gateway "Response is stale"`)
	}
	h.native.writeSimpleProject(w, r, project, merged)
	resolver.auditResolution(r.Context(), group, repository.FormatPyPI, project, strings.ToLower(r.Method), actor, repository.AuditResolved, http.StatusOK)
}

func (h v2GroupPyPIHandler) download(w http.ResponseWriter, r *http.Request, resolver v2GroupResolver, group repository.HostedGroup, members []repository.Member, filename, actor string) {
	for _, member := range members {
		repo, err := resolver.repos.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			continue
		}
		if _, err = h.native.store.GetPyPIFile(r.Context(), repo.ID, filename); errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			h.native.writeError(w, http.StatusServiceUnavailable, "distribution unavailable")
			return
		}
		h.native.downloadForTarget(w, r, repo, filename, actor, pypiAuditTarget{GroupName: group.Name, Repository: group.Name, MemberName: repo.Name})
		return
	}
	resolver.auditResolution(r.Context(), group, repository.FormatPyPI, filename, strings.ToLower(r.Method), actor, repository.AuditNotFound, http.StatusNotFound)
	http.NotFound(w, r)
}
