package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func (h generatedRepositoryAPIAdapter) BrowseRepository(w http.ResponseWriter, r *http.Request, repositoryID adminopenapi.RepositoryId, params adminopenapi.BrowseRepositoryParams) {
	h.withRepositoryBrowseScope(w, r, repositoryID.String(), func(principal Principal, repo repository.HostedRepository) {
		if repo.Format != repository.FormatMaven && repo.Format != repository.FormatRaw {
			writeHostedProblem(w, http.StatusBadRequest, "unsupported_format", "directory browsing is currently available for Maven and Raw repositories")
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
			items, err = h.listProxyRepositoryBrowseNodes(r.Context(), repo, parent, pageSize+1, after)
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

func (h generatedRepositoryAPIAdapter) listProxyRepositoryBrowseNodes(ctx context.Context, repo repository.HostedRepository, parent repository.ArtifactBrowseParent, limit int, after string) ([]repository.ArtifactBrowseNode, error) {
	if h.proxyCache.maintenance == nil {
		return nil, errors.New("proxy cache browsing is not enabled")
	}
	items, err := h.proxyCache.proxyCacheItems(ctx, repo, "", "asset", "all")
	if err != nil {
		return nil, err
	}
	var nodes []repository.ArtifactBrowseNode
	switch repo.Format {
	case repository.FormatMaven:
		nodes = projectMavenProxyBrowseNodes(items, parent)
	case repository.FormatRaw:
		nodes = projectRawProxyBrowseNodes(items, parent)
	default:
		return nil, repository.ErrUnsupportedBrowseFormat
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Key < nodes[j].Key })
	start := sort.Search(len(nodes), func(index int) bool { return nodes[index].Key > after })
	end := min(start+limit, len(nodes))
	return append([]repository.ArtifactBrowseNode(nil), nodes[start:end]...), nil
}

func projectMavenProxyBrowseNodes(items []proxyCacheBrowseItem, parent repository.ArtifactBrowseParent) []repository.ArtifactBrowseNode {
	nodes := make(map[string]repository.ArtifactBrowseNode)
	for _, item := range items {
		parsed, ok := parseMavenCacheCoordinate(item.Path)
		if !ok {
			continue
		}
		coordinate := parsed.GroupID + ":" + parsed.ArtifactID + ":" + parsed.Version
		switch parent.Kind {
		case "":
			nodes[parsed.GroupID] = repository.ArtifactBrowseNode{Key: parsed.GroupID, Kind: repository.BrowseNodeNamespace, Name: parsed.GroupID, HasChildren: true, Namespace: parsed.GroupID}
		case repository.BrowseNodeNamespace:
			if parsed.GroupID == parent.Namespace {
				nodes[parsed.ArtifactID] = repository.ArtifactBrowseNode{Key: parsed.ArtifactID, Kind: repository.BrowseNodeComponent, Name: parsed.ArtifactID, HasChildren: true, Namespace: parsed.GroupID, Component: parsed.ArtifactID}
			}
		case repository.BrowseNodeComponent:
			if parsed.GroupID == parent.Namespace && parsed.ArtifactID == parent.Component {
				nodes[coordinate] = repository.ArtifactBrowseNode{Key: coordinate, Kind: repository.BrowseNodeVersion, Name: parsed.Version, HasChildren: true, Namespace: parsed.GroupID, Component: parsed.ArtifactID, Version: parsed.Version, Coordinate: coordinate}
			}
		case repository.BrowseNodeVersion:
			if coordinate == parent.Version {
				nodes[item.Path] = repository.ArtifactBrowseNode{Key: item.Path, Kind: repository.BrowseNodeAsset, Name: parsed.FileName, Path: item.Path, Coordinate: coordinate, Digest: item.Digest, Size: item.Size, ContentType: item.ContentType}
			}
		}
	}
	return projectedBrowseNodeValues(nodes)
}

func projectRawProxyBrowseNodes(items []proxyCacheBrowseItem, parent repository.ArtifactBrowseParent) []repository.ArtifactBrowseNode {
	prefix := parent.Path
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	nodes := make(map[string]repository.ArtifactBrowseNode)
	for _, item := range items {
		if !strings.HasPrefix(item.Path, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(item.Path, prefix)
		if remainder == "" {
			continue
		}
		if slash := strings.IndexByte(remainder, '/'); slash >= 0 {
			segment := remainder[:slash]
			key := segment + "\x1f0"
			nodes[key] = repository.ArtifactBrowseNode{Key: key, Kind: repository.BrowseNodeDirectory, Name: segment, HasChildren: true, Path: prefix + segment}
			continue
		}
		key := remainder + "\x1f1"
		nodes[key] = repository.ArtifactBrowseNode{Key: key, Kind: repository.BrowseNodeAsset, Name: remainder, Path: item.Path, Coordinate: item.Path, Digest: item.Digest, Size: item.Size, ContentType: item.ContentType}
	}
	return projectedBrowseNodeValues(nodes)
}

func projectedBrowseNodeValues(nodes map[string]repository.ArtifactBrowseNode) []repository.ArtifactBrowseNode {
	items := make([]repository.ArtifactBrowseNode, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, node)
	}
	return items
}

func repositoryBrowsePrincipalKey(principal Principal) string {
	return string(principal.AuthenticationKind) + "\x1f" + principal.Actor
}

func repositoryBrowseParentKey(parent repository.ArtifactBrowseParent) string {
	return strings.Join([]string{string(parent.Kind), parent.Namespace, parent.Component, parent.Version, parent.Path}, "\x1f")
}

func (h hostedRepositoryAPIHandler) decodeRepositoryBrowseParent(token string, repo repository.HostedRepository, principal string) (repository.ArtifactBrowseParent, string, error) {
	if token == "" {
		parent := repository.ArtifactBrowseParent{}
		return parent, repositoryBrowseParentKey(parent), nil
	}
	var cursor repositoryBrowseNodeCursor
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "repository-browse-node" || cursor.RepositoryID != repo.ID || cursor.Format != string(repo.Format) || cursor.Principal != principal || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return repository.ArtifactBrowseParent{}, "", errors.New("invalid parent")
	}
	parent := repository.ArtifactBrowseParent{Kind: repository.BrowseNodeKind(cursor.Kind), Namespace: cursor.Namespace, Component: cursor.Component, Version: cursor.Version, Path: cursor.Path}
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
	if decodeSignedCursor(h.authenticator.AdminToken, token, &cursor) != nil || cursor.Endpoint != "repository-browse-page" || cursor.RepositoryID != repo.ID || cursor.Format != string(repo.Format) || cursor.Principal != principal || cursor.Parent != parent || cursor.After == "" || time.Now().UTC().Unix() >= cursor.ExpiresAt {
		return "", errors.New("invalid page token")
	}
	return cursor.After, nil
}

func (h hostedRepositoryAPIHandler) encodeRepositoryBrowsePageToken(repo repository.HostedRepository, principal, parent, after string) string {
	return encodeSignedCursor(h.authenticator.AdminToken, repositoryBrowsePageCursor{Endpoint: "repository-browse-page", RepositoryID: repo.ID, Format: string(repo.Format), Principal: principal, Parent: parent, After: after, ExpiresAt: time.Now().UTC().Add(15 * time.Minute).Unix()})
}

func (h hostedRepositoryAPIHandler) repositoryBrowseNodeResponse(repo repository.HostedRepository, principal string, node repository.ArtifactBrowseNode) adminopenapi.BrowseNode {
	cursor := repositoryBrowseNodeCursor{
		Endpoint: "repository-browse-node", RepositoryID: repo.ID, Format: string(repo.Format), Principal: principal,
		Kind: string(node.Kind), Namespace: node.Namespace, Component: node.Component, Version: node.Coordinate, Path: node.Path,
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
	response.Coordinate = optionalString(node.Coordinate)
	response.Digest = optionalString(node.Digest)
	response.ContentType = optionalString(node.ContentType)
	if node.Kind == repository.BrowseNodeAsset {
		response.Size = &node.Size
	}
	if !node.CreatedAt.IsZero() {
		createdAt := node.CreatedAt
		response.CreatedAt = &createdAt
	}
	return response
}
