package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
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
	format      repository.Format
	group       bool
	hostedGroup repository.HostedGroup
}

type nexusRepositoryCompatibilityContextKey struct{}

type nexusRepositoryCompatibilityExternalRoute struct {
	name        string
	escapedPath string
	hostedGroup repository.HostedGroup
}

type nexusGoRepositoryCompatibilityHandler struct {
	native nativeGoHandler
}

func (h nexusGoRepositoryCompatibilityHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		if repositoryName, version, ok := parseNexusGoUploadPath(r.URL.EscapedPath()); ok {
			h.native.serveNexusUpload(w, r, repositoryName, version)
			return
		}
	}
	h.native.ServeHTTP(w, r)
}

func (h nexusRepositoryCompatibilityRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, remainder, ok := nexusRepositoryCompatibilityPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	escapedName, escapedRemainder, escapedOK := nexusRepositoryCompatibilityPath(r.URL.EscapedPath())
	decodedEscapedName, unescapeErr := url.PathUnescape(escapedName)
	if !escapedOK || unescapeErr != nil || decodedEscapedName != name {
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
	if target.format == repository.FormatPyPI && !target.group && r.Method == http.MethodPost && remainder == "/" {
		remainder = "/legacy/"
		escapedRemainder = "/legacy/"
	}

	overrideRepositoryRequestClass(r.Context(), target.format)
	externalRoute := nexusRepositoryCompatibilityExternalRoute{name: name, escapedPath: r.URL.EscapedPath(), hostedGroup: target.hostedGroup}
	forwardedContext := context.WithValue(r.Context(), nexusRepositoryCompatibilityContextKey{}, externalRoute)
	forwarded := r.Clone(forwardedContext)
	forwarded.URL.Path = prefix + name + remainder
	forwarded.URL.RawPath = prefix + escapedName + escapedRemainder
	handler.ServeHTTP(w, forwarded)
}

func nexusRepositoryCompatibilityExternalName(ctx context.Context, repositoryName string) (string, bool) {
	route, ok := nexusRepositoryCompatibilityExternalRouteFromContext(ctx, repositoryName)
	return route.name, ok
}

func nexusRepositoryCompatibilityExternalEscapedPath(ctx context.Context, repositoryName string) (string, bool) {
	route, ok := nexusRepositoryCompatibilityExternalRouteFromContext(ctx, repositoryName)
	return route.escapedPath, ok
}

func nexusRepositoryCompatibilityExternalRouteFromContext(ctx context.Context, repositoryName string) (nexusRepositoryCompatibilityExternalRoute, bool) {
	route, ok := ctx.Value(nexusRepositoryCompatibilityContextKey{}).(nexusRepositoryCompatibilityExternalRoute)
	return route, ok && route.name == repositoryName
}

func nexusRepositoryCompatibilityResolvedGroup(ctx context.Context, name string, format repository.Format) (repository.HostedGroup, bool) {
	route, ok := nexusRepositoryCompatibilityExternalRouteFromContext(ctx, name)
	return route.hostedGroup, ok && route.hostedGroup.Name == name && route.hostedGroup.Format == format
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

	group, err := h.groups.GetHostedGroupByName(ctx, name)
	if err != nil {
		return nexusRepositoryCompatibilityTarget{}, err
	}
	return nexusRepositoryCompatibilityTarget{format: group.Format, group: true, hostedGroup: group}, nil
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
