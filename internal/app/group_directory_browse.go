package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

const groupBrowseMemberLimit = 64

type groupBrowseCursor struct {
	Kind      string                          `json:"kind"`
	Scope     string                          `json:"scope"`
	Parent    repository.ArtifactBrowseParent `json:"parent"`
	After     string                          `json:"after,omitempty"`
	ExpiresAt int64                           `json:"expiresAt"`
}

type groupBrowseMember struct {
	repo   repository.HostedRepository
	source adminopenapi.BrowseSource
}

type groupBrowseContribution struct {
	node    repository.ArtifactBrowseNode
	sources []adminopenapi.BrowseSource
}

func (h generatedRepositoryAPIAdapter) BrowseGroup(w http.ResponseWriter, r *http.Request, id adminopenapi.GroupId, params adminopenapi.BrowseGroupParams) {
	group, err := h.groups.GetHostedGroup(r.Context(), id.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "get group failed")
		return
	}
	principal, authenticated := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !authenticated {
		if !group.AnonymousRead || !anonymousAccessAllowed(r.Context(), h.groups) {
			writeHostedProblem(w, 401, "access_denied", "authentication is required")
			return
		}
		principal = anonymousPrincipal()
	}
	if group.Format != repository.FormatMaven && group.Format != repository.FormatRaw {
		writeHostedProblem(w, 400, "unsupported_format", "Group directories support Maven and Raw")
		return
	}
	if len(group.Members) > groupBrowseMemberLimit {
		writeHostedProblem(w, 400, "unsupported_group_size", "Group directories support at most 64 configured members")
		return
	}
	limit := 50
	if params.PageSize != nil {
		limit = int(*params.PageSize)
	}
	if limit < 1 || limit > 200 {
		writeHostedProblem(w, 400, "invalid_request", "pageSize must be between 1 and 200")
		return
	}
	members, scope, mavenScope, err := h.groupBrowseMembers(r, group, principal)
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "resolve Group browse scope failed")
		return
	}
	if len(members) == 0 && !principal.Admin {
		writeHostedProblem(w, 403, "access_denied", "no readable Group members")
		return
	}
	parent := repository.ArtifactBrowseParent{}
	if params.Parent != nil && *params.Parent != "" {
		cursor, valid := h.decodeGroupBrowseCursor(*params.Parent, "node", scope)
		if !valid || !validRepositoryBrowseParent(group.Format, cursor.Parent) {
			writeHostedProblem(w, 400, "invalid_parent", "parent node is invalid or expired")
			return
		}
		parent = cursor.Parent
	}
	after := ""
	if params.PageToken != nil && *params.PageToken != "" {
		cursor, valid := h.decodeGroupBrowseCursor(*params.PageToken, "page", scope)
		if !valid || cursor.Parent != parent || cursor.After == "" {
			writeHostedProblem(w, 400, "invalid_page_token", "page token is invalid or expired")
			return
		}
		after = cursor.After
	}
	contributions, err := h.groupBrowseContributions(r, group, members, mavenScope, parent, limit+1, after)
	if err != nil {
		writeHostedProblem(w, 500, "internal_error", "browse Group failed")
		return
	}
	keys := make([]string, 0, len(contributions))
	for key := range contributions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	more := len(keys) > limit
	if more {
		keys = keys[:limit]
	}
	response := adminopenapi.GroupBrowsePage{GroupId: id, GroupName: group.Name, Format: adminopenapi.GroupBrowsePageFormat(group.Format), Items: make([]adminopenapi.BrowseNode, 0, len(keys)), Candidates: make([]adminopenapi.BrowseSource, 0, len(members))}
	for _, member := range members {
		response.Candidates = append(response.Candidates, member.source)
	}
	for _, key := range keys {
		contribution := contributions[key]
		node := contribution.node
		// A Group node is navigation, not an owner or a selected winning identity.
		result := h.repositoryBrowseNodeResponse(repository.HostedRepository{Format: group.Format}, "", node)
		result.SourceRepositoryId = nil
		result.SourceRepositoryName = nil
		result.Digest = nil
		result.Size = nil
		result.CreatedAt = nil
		result.CachedAt = nil
		result.CacheRepositoryName = nil
		result.Sources = &contribution.sources
		nextParent := repository.ArtifactBrowseParent{Kind: node.Kind, Namespace: node.Namespace, Component: node.Component, Version: node.Coordinate, BuildNumber: node.BuildNumber, Path: node.Path}
		result.Id = h.encodeGroupBrowseCursor("node", scope, nextParent, "")
		response.Items = append(response.Items, result)
	}
	if more {
		token := h.encodeGroupBrowseCursor("page", scope, parent, keys[len(keys)-1])
		response.NextPageToken = &token
	}
	writeNativeMavenJSON(w, 200, response)
}

// Navigation includes grants and all repository configurations, not merely
// the members that happened to contribute a cached node to this page.
func (h generatedRepositoryAPIAdapter) groupBrowseMembers(r *http.Request, group repository.HostedGroup, principal Principal) ([]groupBrowseMember, string, string, error) {
	resolver := v2GroupResolver{groups: h.groups, repos: h.store, authorizer: h.authorizer}
	resolved, err := resolver.resolveMembers(r.Context(), group)
	if err != nil {
		return nil, "", "", err
	}
	policy, err := h.anonymousAccess.GetAnonymousAccessPolicy(r.Context())
	if err != nil {
		return nil, "", "", err
	}
	revisions := []any{group, principal, policy.Version}
	members := make([]groupBrowseMember, 0, len(resolved))
	proxies := make([]repository.Member, 0)
	for _, member := range resolved {
		repo, err := h.store.GetHostedRepository(r.Context(), member.RepositoryID)
		if err != nil {
			return nil, "", "", err
		}
		grants, err := h.grants.GetRepositoryGrants(r.Context(), repo.ID)
		if err != nil {
			return nil, "", "", err
		}
		revisions = append(revisions, repo, grants.Version)
		if isAnonymous(principal) {
			if !repo.AnonymousRead {
				continue
			}
		} else if !h.authorizer.Authorize(r.Context(), principal, repo, RepositoryRead).Allowed {
			continue
		}
		repositoryID, err := uuid.Parse(repo.ID)
		if err != nil {
			return nil, "", "", err
		}
		source := adminopenapi.BrowseSource{RepositoryId: repositoryID, RepositoryName: repo.Name, Type: adminopenapi.BrowseSourceType(repo.Type), ResolutionOrder: len(members) + 1}
		members = append(members, groupBrowseMember{repo: repo, source: source})
		if member.Type == repository.MemberProxy {
			proxies = append(proxies, member)
		}
	}
	mavenScope := ""
	if group.Format == repository.FormatMaven && h.mavenProxy.cache != nil {
		mavenScope = (MavenHandler{Cache: h.mavenProxy.cache}).mavenResolutionScope(proxies)
	}
	revisions = append(revisions, mavenScope)
	encoded, err := json.Marshal(revisions)
	if err != nil {
		return nil, "", "", err
	}
	digest := sha256.Sum256(encoded)
	return members, hex.EncodeToString(digest[:]), mavenScope, nil
}

func (h generatedRepositoryAPIAdapter) groupBrowseContributions(r *http.Request, group repository.HostedGroup, members []groupBrowseMember, mavenScope string, parent repository.ArtifactBrowseParent, limit int, after string) (map[string]*groupBrowseContribution, error) {
	result := make(map[string]*groupBrowseContribution)
	for _, member := range members {
		var nodes []repository.ArtifactBrowseNode
		var err error
		if member.repo.Type == repository.RepositoryTypeProxy {
			store := h.proxyCache.directoryStore()
			if store == nil {
				return nil, errors.New("indexed proxy browsing is unavailable")
			}
			nodes, err = store.ListGroupProxyBrowseNodes(r.Context(), member.repo, group.Name, mavenScope, parent, limit, after)
		} else {
			nodes, err = h.browse.ListGroupArtifactBrowseNodes(r.Context(), member.repo.ID, group.Format, parent, limit, after)
		}
		if errors.Is(err, repository.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			if group.Format == repository.FormatMaven && parent.Kind == repository.BrowseNodeVersion {
				// Build numbers alone are not globally unique across repositories.
				// The signed version prefix also pins the timestamp of every asset.
				if !strings.HasPrefix(node.Path, parent.Path) {
					continue
				}
				node.BuildNumber = parent.BuildNumber
			}
			source := member.source
			source.Coordinate = optionalString(node.Coordinate)
			source.Path = optionalString(node.Path)
			source.Digest = optionalString(node.Digest)
			if node.Kind == repository.BrowseNodeAsset {
				size := node.Size
				source.Size = &size
			}
			if node.BuildNumber > 0 {
				build := node.BuildNumber
				source.BuildNumber = &build
			}
			if member.repo.Type == repository.RepositoryTypeProxy {
				source.CacheRepositoryName = optionalString(node.CacheRepositoryName)
				if !node.CreatedAt.IsZero() {
					cached := node.CreatedAt
					source.CachedAt = &cached
				}
			}
			existing := result[node.Key]
			if existing == nil {
				existing = &groupBrowseContribution{node: node}
				result[node.Key] = existing
			}
			existing.sources = append(existing.sources, source)
		}
	}
	return result, nil
}

func (h generatedRepositoryAPIAdapter) encodeGroupBrowseCursor(kind, scope string, parent repository.ArtifactBrowseParent, after string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, groupBrowseCursor{Kind: kind, Scope: scope, Parent: parent, After: after, ExpiresAt: time.Now().Add(15 * time.Minute).Unix()})
}

func (h generatedRepositoryAPIAdapter) decodeGroupBrowseCursor(token, kind, scope string) (groupBrowseCursor, bool) {
	var cursor groupBrowseCursor
	if len(token) > 16384 || decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Kind != kind || cursor.Scope != scope || cursor.ExpiresAt <= time.Now().Unix() {
		return cursor, false
	}
	return cursor, true
}
