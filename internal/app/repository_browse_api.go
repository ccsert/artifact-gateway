package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func (h generatedRepositoryAPIAdapter) BrowseRepository(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.BrowseRepositoryParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(principal Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatMaven && repo.Format != repository.FormatRaw {
			writeHostedProblem(w, http.StatusBadRequest, "unsupported_format", "directory browsing is currently available for Maven and Raw repositories")
			return
		}
		if repo.Type == repository.RepositoryTypeProxy && h.proxyCache.directoryStore() == nil {
			writeHostedProblem(w, http.StatusBadRequest, "unsupported_repository_type", "directory browsing is not available for this proxy repository")
			return
		}
		pageSize := 50
		if params.PageSize != nil {
			pageSize = int(*params.PageSize)
			if pageSize < 1 || pageSize > 200 {
				writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "pageSize must be between 1 and 200")
				return
			}
		}
		principalKey := repositoryBrowsePrincipalKey(principal)
		parentToken := ""
		if params.Parent != nil {
			parentToken = strings.TrimSpace(*params.Parent)
		}
		parent, parentKey, err := h.decodeRepositoryBrowseParent(parentToken, repo, principalKey)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_parent", "parent node is invalid or expired")
			return
		}
		pageToken := ""
		if params.PageToken != nil {
			pageToken = string(*params.PageToken)
		}
		after, err := h.decodeRepositoryBrowsePageToken(pageToken, repo, principalKey, parentKey)
		if err != nil {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_page_token", "page token is invalid or expired")
			return
		}
		var items []repository.ArtifactBrowseNode
		if repo.Type == repository.RepositoryTypeProxy {
			items, err = h.proxyCache.directoryStore().ListProxyBrowseNodes(r.Context(), repo, parent, pageSize+1, after)
		} else {
			items, err = h.browse.ListArtifactBrowseNodes(r.Context(), repo.ID, repo.Format, parent, pageSize+1, after)
		}
		if errors.Is(err, repository.ErrNotFound) {
			items, err = []repository.ArtifactBrowseNode{}, nil
		}
		if errors.Is(err, repository.ErrUnsupportedBrowseFormat) {
			writeHostedProblem(w, http.StatusBadRequest, "unsupported_format", "directory browsing is not available for this repository format")
			return
		}
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "browse repository failed")
			return
		}
		hasMore := len(items) > pageSize
		if hasMore {
			items = items[:pageSize]
		}
		response := adminopenapi.BrowseNodePage{Items: make([]adminopenapi.BrowseNode, 0, len(items))}
		for _, item := range items {
			response.Items = append(response.Items, h.repositoryBrowseNodeResponse(repo, principalKey, item))
		}
		if hasMore && len(items) > 0 {
			next := h.encodeRepositoryBrowsePageToken(repo, principalKey, parentKey, items[len(items)-1].Key)
			response.NextPageToken = &next
		}
		writeNativeMavenJSON(w, http.StatusOK, response)
	})
}

func repositoryBrowsePrincipalKey(principal Principal) string {
	return string(principal.AuthenticationKind) + "\x1f" + principal.Actor
}

func repositoryBrowseParentKey(parent repository.ArtifactBrowseParent) string {
	return strings.Join([]string{string(parent.Kind), parent.Namespace, parent.Component, parent.Version, strconv.Itoa(parent.BuildNumber), parent.Path}, "\x1f")
}

func (h hostedRepositoryAPIHandler) decodeRepositoryBrowseParent(token string, repo repository.HostedRepository, principal string) (repository.ArtifactBrowseParent, string, error) {
	if token == "" {
		parent := repository.ArtifactBrowseParent{}
		return parent, repositoryBrowseParentKey(parent), nil
	}
	var cursor repositoryBrowseNodeCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "repository-browse-node" || cursor.RepositoryID != repo.ID || cursor.Format != string(repo.Format) || cursor.Principal != principal || cursor.Revision != repositoryBrowseRevision(repo) || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return repository.ArtifactBrowseParent{}, "", errors.New("invalid parent")
	}
	parent := repository.ArtifactBrowseParent{Kind: repository.BrowseNodeKind(cursor.Kind), Namespace: cursor.Namespace, Component: cursor.Component, Version: cursor.Version, BuildNumber: cursor.BuildNumber, Path: cursor.Path}
	if !validRepositoryBrowseParent(repo.Format, parent) {
		return repository.ArtifactBrowseParent{}, "", errors.New("invalid parent")
	}
	return parent, repositoryBrowseParentKey(parent), nil
}

func validRepositoryBrowseParent(format repository.Format, parent repository.ArtifactBrowseParent) bool {
	switch format {
	case repository.FormatMaven:
		switch parent.Kind {
		case repository.BrowseNodeNamespace:
			return parent.Namespace != "" && parent.Component == "" && parent.Version == ""
		case repository.BrowseNodeComponent:
			return parent.Namespace != "" && parent.Component != "" && parent.Version == ""
		case repository.BrowseNodeVersion:
			return parent.Namespace != "" && parent.Component != "" && parent.Version != ""
		}
	case repository.FormatRaw:
		return parent.Kind == repository.BrowseNodeDirectory && parent.Path != "" && !strings.HasPrefix(parent.Path, "/")
	}
	return false
}

func (h hostedRepositoryAPIHandler) decodeRepositoryBrowsePageToken(token string, repo repository.HostedRepository, principal, parent string) (string, error) {
	if token == "" {
		return "", nil
	}
	var cursor repositoryBrowsePageCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "repository-browse-page" || cursor.RepositoryID != repo.ID || cursor.Format != string(repo.Format) || cursor.Principal != principal || cursor.Revision != repositoryBrowseRevision(repo) || cursor.Parent != parent || cursor.After == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid page token")
	}
	return cursor.After, nil
}

func (h hostedRepositoryAPIHandler) encodeRepositoryBrowsePageToken(repo repository.HostedRepository, principal, parent, after string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, repositoryBrowsePageCursor{Endpoint: "repository-browse-page", RepositoryID: repo.ID, Format: string(repo.Format), Principal: principal, Parent: parent, After: after, Revision: repositoryBrowseRevision(repo), ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) repositoryBrowseNodeResponse(repo repository.HostedRepository, principal string, node repository.ArtifactBrowseNode) adminopenapi.BrowseNode {
	cursor := repositoryBrowseNodeCursor{
		Endpoint: "repository-browse-node", RepositoryID: repo.ID, Format: string(repo.Format), Principal: principal,
		Revision: repositoryBrowseRevision(repo), Kind: string(node.Kind), Namespace: node.Namespace, Component: node.Component, Version: node.Coordinate, BuildNumber: node.BuildNumber, Path: node.Path,
		ExpiresAt: time.Now().UTC().Add(time.Hour).Unix(),
	}
	name := node.Name
	if repo.Format == repository.FormatRaw {
		if decoded, err := url.PathUnescape(name); err == nil {
			name = decoded
		}
	}
	response := adminopenapi.BrowseNode{Id: encodeSignedCursor(h.authenticator.AdminToken, cursor), Kind: adminopenapi.BrowseNodeKind(node.Kind), Name: name, HasChildren: node.HasChildren}
	response.Path = optionalString(node.Path)
	response.CacheRepositoryName = optionalString(node.CacheRepositoryName)
	if id, err := uuid.Parse(repo.ID); err == nil {
		response.SourceRepositoryId = &id
	}
	response.SourceRepositoryName = &repo.Name
	if node.BuildNumber > 0 {
		response.BuildNumber = &node.BuildNumber
	}
	response.Coordinate = optionalString(node.Coordinate)
	response.Digest = optionalString(node.Digest)
	response.ContentType = optionalString(node.ContentType)
	if node.Kind == repository.BrowseNodeAsset {
		response.Size = &node.Size
	}
	if !node.CreatedAt.IsZero() {
		createdAt := node.CreatedAt
		if repo.Type == repository.RepositoryTypeProxy {
			response.CachedAt = &createdAt
		} else {
			response.CreatedAt = &createdAt
		}
	}
	if repo.Type == repository.RepositoryTypeProxy && node.Kind == repository.BrowseNodeAsset {
		cached := adminopenapi.Cached
		response.CacheState = &cached
	}
	return response
}

// Proxy parents and cursors expire immediately when their upstream/configuration
// changes. Hosted cursors retain their published grammar.
func repositoryBrowseRevision(repo repository.HostedRepository) string {
	if repo.Type != repository.RepositoryTypeProxy {
		return ""
	}
	hash := sha256.Sum256([]byte(repo.Version + "\x1f" + repo.Endpoint))
	return hex.EncodeToString(hash[:])
}
