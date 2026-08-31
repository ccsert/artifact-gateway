package repository

import (
	"context"
	"sort"
	"strings"
)

func (s *MemoryStore) ListArtifactBrowseNodes(_ context.Context, repositoryID string, format Format, parent ArtifactBrowseParent, limit int, after string) ([]ArtifactBrowseNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 50
	}
	var nodes []ArtifactBrowseNode
	switch format {
	case FormatMaven:
		nodes = s.listMemoryMavenBrowseNodes(repositoryID, parent)
	case FormatRaw:
		nodes = s.listMemoryRawBrowseNodes(repositoryID, parent)
	default:
		return nil, ErrUnsupportedBrowseFormat
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Key < nodes[j].Key })
	start := sort.Search(len(nodes), func(index int) bool { return nodes[index].Key > after })
	end := min(start+limit, len(nodes))
	return append([]ArtifactBrowseNode(nil), nodes[start:end]...), nil
}

func (s *MemoryStore) listMemoryMavenBrowseNodes(repositoryID string, parent ArtifactBrowseParent) []ArtifactBrowseNode {
	artifacts := make([]MavenArtifact, 0)
	for _, artifact := range s.mavenArtifacts {
		if artifact.RepositoryID == repositoryID && artifact.State == "visible" {
			artifacts = append(artifacts, artifact)
		}
	}
	nodes := make(map[string]ArtifactBrowseNode)
	switch parent.Kind {
	case "":
		for _, artifact := range artifacts {
			parts := strings.Split(artifact.Coordinate, ":")
			if len(parts) < 3 || parts[0] == "" {
				continue
			}
			nodes[parts[0]] = ArtifactBrowseNode{Key: parts[0], Kind: BrowseNodeNamespace, Name: parts[0], HasChildren: true, Namespace: parts[0]}
		}
	case BrowseNodeNamespace:
		for _, artifact := range artifacts {
			parts := strings.Split(artifact.Coordinate, ":")
			if len(parts) < 3 || parts[0] != parent.Namespace || parts[1] == "" {
				continue
			}
			nodes[parts[1]] = ArtifactBrowseNode{Key: parts[1], Kind: BrowseNodeComponent, Name: parts[1], HasChildren: true, Namespace: parts[0], Component: parts[1]}
		}
	case BrowseNodeComponent:
		for _, artifact := range artifacts {
			parts := strings.Split(artifact.Coordinate, ":")
			if len(parts) < 3 || parts[0] != parent.Namespace || parts[1] != parent.Component {
				continue
			}
			current, exists := nodes[artifact.Coordinate]
			if !exists || artifact.CreatedAt.After(current.CreatedAt) {
				nodes[artifact.Coordinate] = ArtifactBrowseNode{
					Key: artifact.Coordinate, Kind: BrowseNodeVersion, Name: parts[2], HasChildren: true,
					Namespace: parts[0], Component: parts[1], Version: parts[2], Coordinate: artifact.Coordinate,
					Digest: artifact.Digest, CreatedAt: artifact.CreatedAt,
				}
			}
		}
	case BrowseNodeVersion:
		for _, asset := range s.mavenAssets {
			if asset.RepositoryID != repositoryID {
				continue
			}
			belongs := false
			for _, artifact := range artifacts {
				if artifact.Coordinate == parent.Version && mavenAssetBelongsToArtifactBuild(asset, artifact) {
					belongs = true
					break
				}
			}
			if !belongs {
				continue
			}
			name := asset.Path
			if slash := strings.LastIndex(name, "/"); slash >= 0 {
				name = name[slash+1:]
			}
			nodes[asset.Path] = ArtifactBrowseNode{Key: asset.Path, Kind: BrowseNodeAsset, Name: name, Path: asset.Path, Coordinate: parent.Version, Digest: asset.Digest, Size: asset.Size}
		}
	}
	return browseNodeMapValues(nodes)
}

func (s *MemoryStore) listMemoryRawBrowseNodes(repositoryID string, parent ArtifactBrowseParent) []ArtifactBrowseNode {
	prefix := parent.Path
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	nodes := make(map[string]ArtifactBrowseNode)
	for _, asset := range s.rawAssets {
		if asset.RepositoryID != repositoryID || !strings.HasPrefix(asset.Path, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(asset.Path, prefix)
		if remainder == "" {
			continue
		}
		if slash := strings.IndexByte(remainder, '/'); slash >= 0 {
			segment := remainder[:slash]
			path := prefix + segment
			key := segment + "\x1f0"
			nodes[key] = ArtifactBrowseNode{Key: key, Kind: BrowseNodeDirectory, Name: segment, HasChildren: true, Path: path}
			continue
		}
		key := remainder + "\x1f1"
		nodes[key] = ArtifactBrowseNode{
			Key: key, Kind: BrowseNodeAsset, Name: remainder, Path: asset.Path, Coordinate: asset.Path,
			Digest: asset.Digest, Size: asset.Size, ContentType: asset.ContentType, CreatedAt: asset.UpdatedAt,
		}
	}
	return browseNodeMapValues(nodes)
}

func browseNodeMapValues(nodes map[string]ArtifactBrowseNode) []ArtifactBrowseNode {
	items := make([]ArtifactBrowseNode, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, node)
	}
	return items
}
