package app

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// v2GroupResolver resolves protocol requests against a V2 HostedGroup. A V2
// group mixes hosted and proxy repositories; resolution mirrors the legacy
// Group semantics: every hosted member is tried before any proxy member, and
// members of the same kind keep their configured position order.
type v2GroupResolver struct {
	groups repository.HostedGroupStore
	repos  repository.HostedRepositoryStore
	audit  repository.Store
}

// v2GroupName extracts the first path segment after the format's protocol
// prefix, which is where a V2 group name can appear.
func v2GroupName(format repository.Format, path string) string {
	var prefix string
	switch format {
	case repository.FormatOCI:
		prefix = "/v2/"
	case repository.FormatRaw:
		prefix = "/raw/"
	case repository.FormatMaven:
		prefix = "/maven/"
	case repository.FormatConan:
		rest := strings.TrimPrefix(path, "/conan/v2/")
		if rest == path || rest == "" {
			return ""
		}
		return strings.SplitN(rest, "/", 2)[0]
	default:
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if rest == path || rest == "" {
		return ""
	}
	if format == repository.FormatOCI && rest == "_catalog" {
		return ""
	}
	return strings.SplitN(rest, "/", 2)[0]
}

// resolveHostedGroup returns the V2 group with this name and format, or
// ErrNotFound when no such group exists.
func (r v2GroupResolver) resolveHostedGroup(ctx context.Context, name string, format repository.Format) (repository.HostedGroup, error) {
	if r.groups == nil {
		return repository.HostedGroup{}, repository.ErrNotFound
	}
	after := ""
	for {
		groups, next, err := r.groups.ListHostedGroups(ctx, 200, after)
		if err != nil {
			return repository.HostedGroup{}, err
		}
		for _, group := range groups {
			if group.Name == name && group.Format == format {
				return group, nil
			}
		}
		if next == "" {
			return repository.HostedGroup{}, repository.ErrNotFound
		}
		after = next
	}
}

// resolveMembers expands a V2 group's members into the legacy Member shape,
// hosted members first. Members whose repository is missing, of the wrong
// format, or no longer active are skipped. A proxy member keeps its upstream
// endpoint; a hosted member is marked by type and its repository binding, and
// is served by the format's native hosted path rather than an upstream fetch.
func (r v2GroupResolver) resolveMembers(ctx context.Context, group repository.HostedGroup) ([]repository.Member, error) {
	sorted := append([]repository.GroupMember(nil), group.Members...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })
	hosted := make([]repository.Member, 0, len(sorted))
	proxy := make([]repository.Member, 0, len(sorted))
	for _, member := range sorted {
		repo, err := r.repos.GetHostedRepository(ctx, member.RepositoryID)
		if err != nil || repo.Format != group.Format || repo.State != repository.RepositoryActive {
			continue
		}
		resolved := repository.Member{
			Name:         repo.Name,
			Position:     member.Position,
			RepositoryID: repo.ID,
			AllowedHosts: repo.AllowedHosts,
		}
		switch repo.Type {
		case repository.RepositoryTypeProxy:
			resolved.Type = repository.MemberProxy
			resolved.Endpoint = repo.Endpoint
			proxy = append(proxy, resolved)
		default:
			resolved.Type = repository.MemberHosted
			hosted = append(hosted, resolved)
		}
	}
	return append(hosted, proxy...), nil
}

func (r v2GroupResolver) auditResolution(ctx context.Context, group repository.HostedGroup, format repository.Format, resource, operation, actor string, outcome repository.AuditOutcome, status int) {
	if r.audit == nil {
		return
	}
	_ = r.audit.RecordAudit(ctx, repository.AuditRecord{
		GroupName: group.Name, Repository: group.Name, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(),
		Format: string(format), Resource: resource, Operation: operation, Status: status, CacheDisposition: "bypass",
	})
}

// v2GroupRouter sits between the hosted repository guard and the legacy
// handlers. When the first path segment names a V2 HostedGroup of this format,
// the request is resolved against the group's members; anything else falls
// through to the next handler unchanged.
type v2GroupRouter struct {
	format repository.Format
	groups repository.HostedGroupStore
	repos  repository.HostedRepositoryStore
	audit  repository.Store
	auth   Authenticator
	oci    *v2GroupOCIHandler
	maven  *v2GroupMavenHandler
	raw    *v2GroupRawHandler
	conan  *v2GroupConanHandler
	next   http.Handler
}

func (r v2GroupRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	name := v2GroupName(r.format, req.URL.Path)
	if name == "" {
		r.next.ServeHTTP(w, req)
		return
	}
	resolver := v2GroupResolver{groups: r.groups, repos: r.repos, audit: r.audit}
	group, err := resolver.resolveHostedGroup(req.Context(), name, r.format)
	if errors.Is(err, repository.ErrNotFound) {
		r.next.ServeHTTP(w, req)
		return
	}
	if err != nil {
		http.Error(w, "group lookup failed", http.StatusServiceUnavailable)
		return
	}
	switch r.format {
	case repository.FormatOCI:
		if r.oci != nil {
			r.oci.serve(w, req, resolver, group)
			return
		}
	case repository.FormatMaven:
		if r.maven != nil {
			r.maven.serve(w, req, resolver, group)
			return
		}
	case repository.FormatRaw:
		if r.raw != nil {
			r.raw.serve(w, req, resolver, group)
			return
		}
	case repository.FormatConan:
		if r.conan != nil {
			r.conan.serve(w, req, resolver, group)
			return
		}
	}
	r.next.ServeHTTP(w, req)
}
