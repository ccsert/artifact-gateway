package nuget

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func groupRepository(group repository.HostedGroup) repository.HostedRepository {
	return repository.HostedRepository{ID: group.ID, Name: group.Name, Format: repository.FormatNuGet, Type: repository.RepositoryTypeProxy, State: repository.RepositoryActive, AnonymousRead: group.AnonymousRead, Version: group.Version}
}
func (m *Module) groupMembers(ctx context.Context, group repository.HostedGroup) ([]repository.HostedRepository, error) {
	if m.sources == nil || m.groups == nil || len(group.Members) == 0 || len(group.Members) > 32 {
		return nil, repository.ErrDisabled
	}
	ordered := append([]repository.GroupMember(nil), group.Members...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Position < ordered[j].Position })
	out := make([]repository.HostedRepository, 0, len(ordered))
	seen := map[string]bool{}
	for _, member := range ordered {
		if seen[member.RepositoryID] {
			return nil, repository.ErrNuGetSourceChanged
		}
		seen[member.RepositoryID] = true
		repo, err := m.repository(ctx, member.RepositoryID, true)
		if err != nil {
			return nil, repository.ErrNuGetSourceChanged
		}
		out = append(out, repo)
	}
	return out, nil
}
func (m *Module) authorizeGroup(w http.ResponseWriter, r *http.Request, group repository.HostedGroup, members []repository.HostedRepository, resource string, collection bool) (authorization.Principal, bool) {
	p, ok := m.principal(r)
	if !ok {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-NuGet-ApiKey") != "" || !group.AnonymousRead || m.anonymous == nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway NuGet"`)
			m.problem(w, 401, "authentication_required")
			return p, false
		}
		policy, err := m.anonymous.GetAnonymousAccessPolicy(r.Context())
		if err != nil || !policy.Enabled {
			m.problem(w, 401, "authentication_required")
			return p, false
		}
		p = authorization.Principal{Actor: "anonymous"}
	}
	// The same caller must pass every ordered member's read boundary before
	// discovery. A denied private owner cannot become a public fallback.
	for _, repo := range members {
		if _, ok = m.authorizeRequest(w, r, repo, authorization.RepositoryRead, resource, collection); !ok {
			return p, false
		}
	}
	return p, true
}
func (m *Module) groupMetadata(ctx context.Context, group repository.HostedGroup, members []repository.HostedRepository, id string, query url.Values, autocomplete bool, readable func(string) bool) ([]repository.NuGetPublication, error) {
	all := map[string]map[string]repository.NuGetPublication{}
	first := map[string]repository.NuGetPublication{}
	memberByID := map[string]repository.HostedRepository{}
	for _, member := range members {
		memberByID[member.ID] = member
		var values []repository.NuGetPublication
		var err error
		if id != "" {
			values, err = m.packageMetadata(ctx, member, id)
		} else {
			values, err = m.searchMetadata(ctx, member, query, autocomplete)
		}
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			key := value.PackageID + "@" + value.Version
			if len(first) >= 4096 && first[key].Version == "" {
				return nil, errMetadataLimit
			}
			if all[key] == nil {
				all[key] = map[string]repository.NuGetPublication{}
				first[key] = value
			}
			all[key][member.ID] = value
		}
	}
	keys := make([]string, 0, len(first))
	for key := range first {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]repository.NuGetPublication, 0, len(keys))
	for _, key := range keys {
		candidate := first[key]
		// Collection access can authorize discovery without granting this full
		// package ID. Do not let a filtered result establish a global authority.
		if readable != nil && !readable(candidate.PackageID) {
			continue
		}
		member := memberByID[candidate.RepositoryID]
		owner, err := m.sources.BindNuGetGroupAuthority(ctx, group.Version, repository.NuGetGroupAuthority{GroupID: group.ID, PackageID: candidate.PackageID, Version: candidate.Version, RepositoryID: member.ID, RepositoryVersion: member.Version})
		if err != nil {
			return nil, err
		}
		selected, ok := all[key][owner.RepositoryID]
		if !ok {
			return nil, repository.ErrNotFound
		}
		if memberByID[owner.RepositoryID].Version != owner.RepositoryVersion {
			return nil, repository.ErrNuGetSourceChanged
		}
		if owner.Digest != "" && selected.Digest != "" && (selected.Digest != owner.Digest || selected.Size != owner.Size) {
			return nil, repository.ErrNuGetPublicationConflict
		}
		out = append(out, selected)
	}
	return out, nil
}
func (m *Module) groupPackage(ctx context.Context, group repository.HostedGroup, members []repository.HostedRepository, id, version string) (repository.NuGetPublication, error) {
	items, err := m.groupMetadata(ctx, group, members, id, nil, false, nil)
	if err != nil {
		return repository.NuGetPublication{}, err
	}
	var selected repository.NuGetPublication
	for _, value := range items {
		if value.Version == version {
			selected = value
			break
		}
	}
	if selected.Version == "" {
		return selected, repository.ErrNotFound
	}
	var member repository.HostedRepository
	for _, r := range members {
		if r.ID == selected.RepositoryID {
			member = r
			break
		}
	}
	if member.Type == repository.RepositoryTypeProxy {
		selected, err = m.proxyPackage(ctx, member, id, version)
	} else {
		selected, err = m.store.GetNuGetPublication(ctx, member.ID, id, version)
		if err == nil {
			selected, err = m.verifyPublication(ctx, selected)
		}
	}
	if err != nil {
		return selected, err
	}
	owner, err := m.sources.BindNuGetGroupAuthority(ctx, group.Version, repository.NuGetGroupAuthority{GroupID: group.ID, PackageID: id, Version: version, RepositoryID: member.ID, RepositoryVersion: member.Version, Digest: selected.Digest, Size: selected.Size})
	if err != nil {
		return selected, err
	}
	if owner.RepositoryID != member.ID || owner.Digest != selected.Digest || owner.Size != selected.Size {
		return selected, repository.ErrNuGetPublicationConflict
	}
	return selected, nil
}
func (m *Module) serveGroup(w http.ResponseWriter, r *http.Request, group repository.HostedGroup, path []string) {
	if len(path) < 2 || path[0] != "v3" {
		m.problem(w, 404, "not_found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		m.problem(w, 405, "read_only_source")
		return
	}
	parts := path[1:]
	resource := ""
	if len(parts) > 1 && (parts[0] == "flat" || parts[0] == "registration") {
		id, err := nugetprotocol.NormalizeID(parts[1])
		if err != nil {
			m.problem(w, 404, "not_found")
			return
		}
		resource = id
	}
	members, err := m.groupMembers(r.Context(), group)
	if err != nil {
		m.failure(w, err)
		return
	}
	principal, ok := m.authorizeGroup(w, r, group, members, resource, resource == "")
	if !ok {
		return
	}
	repo := groupRepository(group)
	if len(parts) == 1 && parts[0] == "index.json" {
		for _, member := range members {
			if member.Type == repository.RepositoryTypeProxy {
				if _, err := m.resources(r.Context(), member); err != nil {
					m.failure(w, err)
					return
				}
			}
		}
		m.serviceIndex(w, r, repo)
		return
	}
	if len(parts) == 1 && (parts[0] == "query" || parts[0] == "autocomplete") {
		skip, take, err := searchParameters(r)
		if err != nil {
			m.problem(w, 400, "invalid_search_query")
			return
		}
		readable := func(id string) bool {
			if principal.Actor != "anonymous" {
				for _, member := range members {
					if !m.access.AuthorizeResource(r.Context(), principal, member, authorization.RepositoryRead, id).Allowed {
						return false
					}
				}
			}
			return true
		}
		items, err := m.groupMetadata(r.Context(), group, members, "", r.URL.Query(), parts[0] == "autocomplete", readable)
		if err != nil {
			m.failure(w, err)
			return
		}
		m.searchItems(w, r, repo, principal, parts[0] == "autocomplete", items, skip, take, false)
		return
	}
	if len(parts) < 3 || resource == "" {
		m.problem(w, 404, "not_found")
		return
	}
	if parts[0] == "flat" && len(parts) == 3 && parts[2] == "index.json" {
		items, err := m.groupMetadata(r.Context(), group, members, resource, nil, false, nil)
		if err != nil {
			m.failure(w, err)
			return
		}
		if len(items) == 0 {
			m.problem(w, 404, "not_found")
			return
		}
		sortedVersions(items)
		versions := make([]string, 0, len(items))
		for _, p := range items {
			versions = append(versions, p.Version)
		}
		m.metadataJSON(w, r, map[string]any{"versions": versions})
		return
	}
	if parts[0] == "registration" {
		items, err := m.groupMetadata(r.Context(), group, members, resource, nil, false, nil)
		if err != nil {
			m.failure(w, err)
			return
		}
		if len(items) == 0 {
			m.problem(w, 404, "not_found")
			return
		}
		m.registrationItems(w, r, repo, parts[1:], items)
		return
	}
	if parts[0] == "flat" && len(parts) == 4 {
		version, err := nugetprotocol.NormalizeVersion(parts[2])
		version = strings.ToLower(version)
		if err != nil {
			m.problem(w, 404, "not_found")
			return
		}
		file := parts[3]
		if file != resource+"."+version+".nupkg" && file != resource+".nuspec" && file != resource+"."+version+".nupkg.sha512" {
			m.problem(w, 404, "not_found")
			return
		}
		p, err := m.groupPackage(r.Context(), group, members, resource, version)
		if err != nil {
			m.failure(w, err)
			return
		}
		w.Header().Set("X-NuGet-Source-Repository", p.RepositoryID)
		m.packageFile(w, r, repo, p, file, principal.Actor)
		_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: principal.Actor, Repository: repo.Name, GroupName: repo.Name, Format: "nuget", Resource: resource + "@" + version, Operation: "nuget.group.resolve", Outcome: repository.AuditResolved, Status: 200, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"repositoryId": p.RepositoryID, "digest": p.Digest}})
		return
	}
	m.problem(w, 404, "not_found")
}
func searchParameters(r *http.Request) (int, int, error) {
	q := r.URL.Query()
	if len(q.Get("q")) > 512 || len(q.Get("id")) > 100 || q.Get("prerelease") != "" && q.Get("prerelease") != "true" && q.Get("prerelease") != "false" || q.Get("semVerLevel") != "" && q.Get("semVerLevel") != "2.0.0" || q.Get("packageType") != "" {
		return 0, 0, errUnsupportedSource
	}
	for _, values := range q {
		if len(values) != 1 {
			return 0, 0, errUnsupportedSource
		}
	}
	skip, take := 0, 20
	for _, entry := range []struct {
		name    string
		target  *int
		maximum int
	}{{"skip", &skip, 10000}, {"take", &take, 100}} {
		if raw := q.Get(entry.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 || value > entry.maximum {
				return 0, 0, errUnsupportedSource
			}
			*entry.target = value
		}
	}
	return skip, take, nil
}
