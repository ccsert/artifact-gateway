package nuget

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	nugetprotocol "github.com/ccsert/artifact-gateway/internal/protocol/nuget"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
	"github.com/google/uuid"
)

type sourceCredential struct {
	Scheme   repository.UpstreamAuthScheme `json:"scheme"`
	Username string                        `json:"username,omitempty"`
	Secret   string                        `json:"secret,omitempty"`
}
type upstreamStatus struct {
	Scheme                repository.UpstreamAuthScheme `json:"scheme"`
	Username              string                        `json:"username,omitempty"`
	CredentialsConfigured bool                          `json:"credentialsConfigured"`
}
type repositoryInput struct {
	Name          string                     `json:"name"`
	AnonymousRead bool                       `json:"anonymousRead"`
	Type          repository.RepositoryType  `json:"type,omitempty"`
	Endpoint      string                     `json:"endpoint,omitempty"`
	AllowedHosts  []string                   `json:"allowedHosts,omitempty"`
	UpstreamAuth  *sourceCredential          `json:"upstreamAuth,omitempty"`
	EgressMode    repository.EgressProxyMode `json:"egressMode,omitempty"`
	Version       string                     `json:"version,omitempty"`
}

func (m *Module) configureRepository(in repositoryInput, existing repository.HostedRepository) (repository.HostedRepository, error) {
	invalid := errors.New("invalid NuGet source configuration")
	if !previewRepositoryName.MatchString(in.Name) {
		return existing, invalid
	}
	if in.Type == "" {
		in.Type = repository.RepositoryTypeHosted
	}
	if existing.ID != "" && (in.Name != existing.Name || in.Type != existing.Type) {
		return existing, invalid
	}
	next := existing
	next.Name, next.Format, next.Type, next.AnonymousRead = in.Name, repository.FormatNuGet, in.Type, in.AnonymousRead
	if in.Type == repository.RepositoryTypeHosted {
		if in.Endpoint != "" || len(in.AllowedHosts) > 0 || in.UpstreamAuth != nil || in.EgressMode != "" {
			return next, invalid
		}
		return next, nil
	}
	if in.Type != repository.RepositoryTypeProxy || m.sources == nil || m.upstream == nil || m.configuration == nil || len(in.AllowedHosts) > 32 {
		return next, invalid
	}
	u, err := url.Parse(in.Endpoint)
	if err != nil || !validSourceURL(u) || u.RawQuery != "" {
		return next, invalid
	}
	seen := map[string]bool{}
	for _, host := range in.AllowedHosts {
		candidate, err := url.Parse("https://" + host)
		if err != nil || !validSourceURL(candidate) || candidate.Path != "" || candidate.RawQuery != "" || strings.TrimSpace(host) != host || seen[strings.ToLower(host)] {
			return next, invalid
		}
		seen[strings.ToLower(host)] = true
	}
	next.Endpoint, next.AllowedHosts = in.Endpoint, append([]string(nil), in.AllowedHosts...)
	mode := in.EgressMode
	if mode == "" {
		mode = repository.EgressProxyModeDirect
	}
	if mode != repository.EgressProxyModeDirect && mode != repository.EgressProxyModeEnvironment {
		return next, invalid
	}
	next.EgressProxy = &repository.EgressProxy{Mode: mode}
	if in.UpstreamAuth != nil {
		request := in.UpstreamAuth
		if len(request.Username) > 512 || len(request.Secret) > 16384 || strings.ContainsAny(request.Username+request.Secret, "\x00\r\n") {
			return next, invalid
		}
		if request.Scheme == repository.UpstreamAuthSchemeNone {
			if request.Username != "" || request.Secret != "" {
				return next, invalid
			}
			next.UpstreamAuth = nil
		} else {
			auth := &repository.UpstreamAuth{Scheme: request.Scheme, Username: request.Username}
			if request.Secret != "" {
				ciphertext, err := secrets.Seal(upstreamauth.SecretPurpose, request.Secret)
				if err != nil {
					return next, invalid
				}
				auth.Secret = ciphertext
			} else if existing.UpstreamAuth != nil && existing.UpstreamAuth.Scheme == request.Scheme {
				auth.Secret = existing.UpstreamAuth.Secret
			}
			if auth.Validate() != nil || auth.Scheme != repository.UpstreamAuthSchemeBasic && auth.Scheme != repository.UpstreamAuthSchemeBearer {
				return next, invalid
			}
			next.UpstreamAuth = auth
		}
	}
	if existing.UpstreamAuth != nil && next.UpstreamAuth != nil && (existing.Endpoint != next.Endpoint || !slices.Equal(existing.AllowedHosts, next.AllowedHosts)) && (in.UpstreamAuth == nil || in.UpstreamAuth.Secret == "") {
		return next, invalid
	}
	if upstreamauth.Validate(next.UpstreamAuth) != nil {
		return next, invalid
	}
	return next, nil
}
func (m *Module) nameLock(ctx context.Context, name, kind string) (context.Context, func(), error) {
	ctx, release, err := m.store.LockNuGetPublication(ctx, "preview-configuration", "name", name)
	if err != nil {
		return ctx, nil, err
	}
	if repo, e := m.repos.GetHostedRepositoryByName(ctx, name); e == nil && kind == "group" && repo.ID != "" {
		release()
		return ctx, nil, repository.ErrNameExists
	} else if e != nil && !errors.Is(e, repository.ErrNotFound) {
		release()
		return ctx, nil, e
	}
	if m.groups != nil {
		if group, e := m.groups.GetHostedGroupByName(ctx, name); e == nil && kind == "repository" && group.ID != "" {
			release()
			return ctx, nil, repository.ErrNameExists
		} else if e != nil && !errors.Is(e, repository.ErrNotFound) {
			release()
			return ctx, nil, e
		}
	}
	return ctx, release, nil
}
func (m *Module) replacePreviewRepository(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, p authorization.Principal) {
	if !p.Admin {
		m.problem(w, 403, "access_denied")
		return
	}
	if m.configuration == nil {
		m.problem(w, 503, "nuget_preview_unavailable")
		return
	}
	var input repositoryInput
	if !decodeBody(w, r, &input) || input.Version == "" {
		m.problem(w, 400, "invalid_source_configuration")
		return
	}
	next, err := m.configureRepository(input, repo)
	if err != nil {
		m.problem(w, 400, "invalid_source_configuration")
		return
	}
	next, err = m.configuration.UpdateHostedRepository(r.Context(), next, input.Version)
	if err != nil {
		m.failure(w, err)
		return
	}
	_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: repo.Name, Format: "nuget", Resource: repo.ID, Operation: "nuget.preview.configure", Outcome: repository.AuditResolved, Status: 200, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"version": next.Version}})
	writeJSON(w, r, 200, repositoryDTO(r, next))
}

type groupInput struct {
	Name          string   `json:"name"`
	AnonymousRead bool     `json:"anonymousRead"`
	Members       []string `json:"members"`
	Version       string   `json:"version,omitempty"`
}

func groupDTO(r *http.Request, g repository.HostedGroup) map[string]any {
	members := make([]string, 0, len(g.Members))
	for _, member := range g.Members {
		members = append(members, member.RepositoryID)
	}
	return map[string]any{"id": g.ID, "name": g.Name, "format": "nuget", "preview": true, "version": g.Version, "anonymousRead": g.AnonymousRead, "members": members, "sourceUrl": sourceBase(r, groupRepository(g)) + "index.json"}
}
func (m *Module) previewGroups(w http.ResponseWriter, r *http.Request, p authorization.Principal) {
	if m.groups == nil || m.sources == nil {
		m.problem(w, 503, "nuget_preview_unavailable")
		return
	}
	const prefix = "/api/v2/previews/nuget/groups"
	tail := strings.TrimPrefix(r.URL.Path, prefix)
	if tail == "" {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		if !p.Admin {
			m.problem(w, 403, "access_denied")
			return
		}
		var input groupInput
		if !decodeBody(w, r, &input) || input.Version != "" {
			m.problem(w, 400, "invalid_group")
			return
		}
		group, err := m.configuredGroup(r.Context(), input)
		if err != nil {
			m.problem(w, 400, "invalid_group")
			return
		}
		ctx, release, err := m.nameLock(r.Context(), group.Name, "group")
		if err != nil {
			m.failure(w, err)
			return
		}
		defer release()
		key, ok := requestKey(r)
		if !ok {
			if r.Header.Get("Idempotency-Key") != "" {
				m.problem(w, 400, "invalid_idempotency_key")
				return
			}
			key = uuid.NewString()
		}
		body, _ := json.Marshal(input)
		sum := sha256.Sum256(body)
		group.ID = uuid.NewString()
		group, replay, err := m.groups.CreateHostedGroupIdempotently(ctx, group, p.Actor, "nuget-preview:"+key, hex.EncodeToString(sum[:]))
		if err != nil {
			m.failure(w, err)
			return
		}
		if !replay {
			_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: group.Name, GroupName: group.Name, Format: "nuget", Resource: group.ID, Operation: "nuget.preview.group.create", Outcome: repository.AuditResolved, Status: 201, OccurredAt: time.Now().UTC()})
		}
		writeJSON(w, r, 201, groupDTO(r, group))
		return
	}
	if !strings.HasPrefix(tail, "/") {
		m.problem(w, 404, "not_found")
		return
	}
	parts := strings.Split(strings.TrimPrefix(tail, "/"), "/")
	if _, err := uuid.Parse(parts[0]); err != nil {
		m.problem(w, 404, "not_found")
		return
	}
	group, err := m.groups.GetHostedGroup(r.Context(), parts[0])
	if err != nil || group.Format != repository.FormatNuGet {
		m.problem(w, 404, "not_found")
		return
	}
	if len(parts) == 5 && parts[1] == "sources" && parts[4] == "revalidate" && r.Method == http.MethodPost {
		m.revalidatePreviewSource(w, r, p, group, parts[2], parts[3])
		return
	}
	if len(parts) == 1 && r.Method == http.MethodPut {
		if !p.Admin {
			m.problem(w, 403, "access_denied")
			return
		}
		var input groupInput
		if !decodeBody(w, r, &input) || input.Version == "" || input.Name != group.Name {
			m.problem(w, 400, "invalid_group")
			return
		}
		next, err := m.configuredGroup(r.Context(), input)
		if err != nil {
			m.problem(w, 400, "invalid_group")
			return
		}
		next.ID = group.ID
		next, err = m.groups.ReplaceHostedGroup(r.Context(), next, input.Version)
		if err != nil {
			m.failure(w, err)
			return
		}
		_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: group.Name, GroupName: group.Name, Format: "nuget", Resource: group.ID, Operation: "nuget.preview.group.configure", Outcome: repository.AuditResolved, Status: 200, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"version": next.Version}})
		writeJSON(w, r, 200, groupDTO(r, next))
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	members, err := m.groupMembers(r.Context(), group)
	if err != nil {
		m.failure(w, err)
		return
	}
	if len(parts) == 1 {
		if _, ok := m.authorizeGroup(w, r, group, members, "", true); ok {
			writeJSON(w, r, 200, groupDTO(r, group))
		}
		return
	}
	if len(parts) != 4 || parts[1] != "sources" {
		m.problem(w, 404, "not_found")
		return
	}
	id, e1 := nugetprotocol.NormalizeID(parts[2])
	version, e2 := nugetprotocol.NormalizeVersion(parts[3])
	if e1 != nil || e2 != nil {
		m.problem(w, 400, "invalid_identity")
		return
	}
	version = strings.ToLower(version)
	if _, ok := m.authorizeGroup(w, r, group, members, id, false); !ok {
		return
	}
	publication, err := m.groupPackage(r.Context(), group, members, id, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	owner, err := m.sources.GetNuGetGroupAuthority(r.Context(), group.ID, id, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	writeJSON(w, r, 200, map[string]any{"groupId": group.ID, "packageId": id, "version": version, "repositoryId": owner.RepositoryID, "repositoryVersion": owner.RepositoryVersion, "digest": publication.Digest, "size": publication.Size})
}

func (m *Module) revalidatePreviewSource(w http.ResponseWriter, r *http.Request, p authorization.Principal, group repository.HostedGroup, rawID, rawVersion string) {
	if !p.Admin {
		m.problem(w, 403, "access_denied")
		return
	}
	var input struct {
		GroupVersion      string `json:"groupVersion"`
		RepositoryVersion string `json:"repositoryVersion"`
	}
	id, e1 := nugetprotocol.NormalizeID(rawID)
	version, e2 := nugetprotocol.NormalizeVersion(rawVersion)
	version = strings.ToLower(version)
	if e1 != nil || e2 != nil || !decodeBody(w, r, &input) || input.GroupVersion == "" || input.RepositoryVersion == "" {
		m.problem(w, 400, "invalid_source_revalidation")
		return
	}
	prior, err := m.sources.GetNuGetGroupAuthority(r.Context(), group.ID, id, version)
	if err != nil {
		m.failure(w, err)
		return
	}
	if prior.Digest == "" {
		m.failure(w, repository.ErrNuGetPublicationConflict)
		return
	}
	members, err := m.groupMembers(r.Context(), group)
	if err != nil {
		m.failure(w, err)
		return
	}
	var source repository.HostedRepository
	for _, member := range members {
		if member.ID == prior.RepositoryID {
			source = member
			break
		}
	}
	if source.ID == "" {
		m.failure(w, repository.ErrNuGetSourceChanged)
		return
	}
	var publication repository.NuGetPublication
	if source.Type == repository.RepositoryTypeProxy {
		publication, err = m.proxyPackage(r.Context(), source, id, version)
	} else {
		publication, err = m.store.GetNuGetPublication(r.Context(), source.ID, id, version)
		if err == nil {
			publication, err = m.verifyPublication(r.Context(), publication)
		}
	}
	if err != nil {
		m.failure(w, err)
		return
	}
	confirmed, err := m.sources.RevalidateNuGetGroupAuthority(r.Context(), input.GroupVersion, input.RepositoryVersion, repository.NuGetGroupAuthority{GroupID: group.ID, PackageID: id, Version: version, RepositoryID: source.ID, RepositoryVersion: source.Version, Digest: publication.Digest, Size: publication.Size})
	if err != nil {
		m.failure(w, err)
		return
	}
	_ = m.audit.RecordAudit(r.Context(), repository.AuditRecord{Actor: p.Actor, Repository: group.Name, GroupName: group.Name, Format: "nuget", Resource: id + "@" + version, Operation: "nuget.preview.source.revalidate", Outcome: repository.AuditResolved, Status: 200, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"repositoryId": source.ID, "repositoryVersion": source.Version, "digest": confirmed.Digest}})
	writeJSON(w, r, 200, map[string]any{"groupId": group.ID, "packageId": id, "version": version, "repositoryId": confirmed.RepositoryID, "repositoryVersion": confirmed.RepositoryVersion, "digest": confirmed.Digest, "size": confirmed.Size})
}
func (m *Module) configuredGroup(ctx context.Context, input groupInput) (repository.HostedGroup, error) {
	if !previewRepositoryName.MatchString(input.Name) || len(input.Members) < 1 || len(input.Members) > 32 {
		return repository.HostedGroup{}, repository.ErrInvalidNuGetPublication
	}
	group := repository.HostedGroup{Name: input.Name, Format: repository.FormatNuGet, AnonymousRead: input.AnonymousRead, Members: []repository.GroupMember{}}
	seen := map[string]bool{}
	for index, id := range input.Members {
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			return group, repository.ErrInvalidNuGetPublication
		}
		seen[id] = true
		if _, err := m.repository(ctx, id, true); err != nil {
			return group, err
		}
		group.Members = append(group.Members, repository.GroupMember{RepositoryID: id, Position: index})
	}
	return group, nil
}
