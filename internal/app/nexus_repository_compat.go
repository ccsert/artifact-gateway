package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// nexusRepositoryCompatibilityRouter keeps protocol handling on the canonical
// Gateway routes while accepting the repository root used by Nexus clients.
// The canonical handlers remain responsible for authentication, authorization,
// auditing, protocol validation, and object lifecycle.
type nexusRepositoryCompatibilityRouter struct {
	repositories repository.HostedRepositoryStore
	groups       repository.HostedGroupStore
	routes       map[repository.Format]nexusRepositoryCompatibilityRoute
}

type nexusRepositoryCompatibilityRoute struct {
	repositoryPrefix  string
	repositoryHandler http.Handler
	groupPrefix       string
	groupHandler      http.Handler
}

type nexusRepositoryCompatibilityTarget struct {
	format repository.Format
	group  bool
}

type nexusRepositoryCompatibilityContextKey struct{}

// nexusRepositoryMavenCanonicalRouter resolves the only ambiguous compatibility
// root: a repository literally named "maven" overlaps the canonical Maven
// prefix. An existing Maven repository in the next segment keeps canonical
// precedence; otherwise the request is resolved as the Nexus-style alias.
type nexusRepositoryMavenCanonicalRouter struct {
	repositories  repository.HostedRepositoryStore
	canonical     http.Handler
	compatibility http.Handler
}

func (h nexusRepositoryMavenCanonicalRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := repositoryNameAfterPrefix(r.URL.Path, "/repository/maven/")
	if name != "" {
		repo, err := h.repositories.GetHostedRepositoryByName(r.Context(), name)
		if err == nil && repo.Format == repository.FormatMaven {
			h.canonical.ServeHTTP(w, r)
			return
		}
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			http.Error(w, "repository lookup failed", http.StatusServiceUnavailable)
			return
		}
	}
	h.compatibility.ServeHTTP(w, r)
}

func (h nexusRepositoryCompatibilityRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, remainder, ok := nexusRepositoryCompatibilityPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	target, err := h.resolveTarget(r.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "repository lookup failed", http.StatusServiceUnavailable)
		return
	}
	route, ok := h.routes[target.format]
	prefix, handler := route.repositoryPrefix, route.repositoryHandler
	if target.group {
		prefix, handler = route.groupPrefix, route.groupHandler
	}
	if !ok || handler == nil {
		http.NotFound(w, r)
		return
	}

	forwardedContext := context.WithValue(r.Context(), nexusRepositoryCompatibilityContextKey{}, name)
	forwarded := r.Clone(forwardedContext)
	forwarded.URL.Path = prefix + name + remainder
	forwarded.URL.RawPath = ""
	handler.ServeHTTP(w, forwarded)
}

func nexusRepositoryCompatibilityExternalName(ctx context.Context, repositoryName string) (string, bool) {
	name, ok := ctx.Value(nexusRepositoryCompatibilityContextKey{}).(string)
	return name, ok && name == repositoryName
}

func (h nexusRepositoryCompatibilityRouter) resolveTarget(ctx context.Context, name string) (nexusRepositoryCompatibilityTarget, error) {
	repo, err := h.repositories.GetHostedRepositoryByName(ctx, name)
	if err == nil {
		return nexusRepositoryCompatibilityTarget{format: repo.Format}, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return nexusRepositoryCompatibilityTarget{}, err
	}
	if h.groups == nil {
		return nexusRepositoryCompatibilityTarget{}, repository.ErrNotFound
	}

	after := ""
	for {
		groups, next, listErr := h.groups.ListHostedGroups(ctx, 200, after)
		if listErr != nil {
			return nexusRepositoryCompatibilityTarget{}, listErr
		}
		for _, group := range groups {
			if group.Name == name {
				return nexusRepositoryCompatibilityTarget{format: group.Format, group: true}, nil
			}
		}
		if next == "" {
			return nexusRepositoryCompatibilityTarget{}, repository.ErrNotFound
		}
		after = next
	}
}

func nexusRepositoryCompatibilityPath(path string) (name, remainder string, ok bool) {
	const prefix = "/repository/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	relative := strings.TrimPrefix(path, prefix)
	separator := strings.IndexByte(relative, '/')
	if separator <= 0 {
		return "", "", false
	}
	return relative[:separator], relative[separator:], true
}

func repositoryNameAfterPrefix(path, prefix string) string {
	relative := strings.TrimPrefix(path, prefix)
	if relative == path || relative == "" {
		return ""
	}
	if separator := strings.IndexByte(relative, '/'); separator >= 0 {
		return relative[:separator]
	}
	return relative
}
