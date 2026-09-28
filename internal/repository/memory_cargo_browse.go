package repository

import (
	"net/url"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func (s *MemoryStore) listMemoryCargoBrowseNodes(repositoryID string, parent ArtifactBrowseParent) []ArtifactBrowseNode {
	nodes := make(map[string]ArtifactBrowseNode)
	for _, publication := range s.cargoPublications {
		if publication.RepositoryID != repositoryID {
			continue
		}
		identity, err := cargo.NormalizeIdentity(publication.Name, publication.Version)
		if err != nil {
			continue
		}
		switch parent.Kind {
		case "":
			nodes[identity.CollisionKey] = ArtifactBrowseNode{
				Key: identity.CollisionKey, Kind: BrowseNodeComponent, Name: publication.Name,
				Component: publication.Name, Coordinate: publication.Name, HasChildren: true,
			}
		case BrowseNodeComponent:
			parentIdentity, err := cargo.NormalizeIdentity(parent.Component, "0.0.0")
			if err != nil || identity.CollisionKey != parentIdentity.CollisionKey {
				continue
			}
			coordinate := publication.Name + "@" + publication.Version
			nodes[identity.VersionKey] = ArtifactBrowseNode{
				Key: identity.VersionKey, Kind: BrowseNodeVersion, Name: publication.Version,
				Component: publication.Name, Version: coordinate, Coordinate: coordinate,
				Digest: publication.Digest, CreatedAt: publication.PublishedAt, HasChildren: true,
			}
		case BrowseNodeVersion:
			if publication.Name+"@"+publication.Version != parent.Version || publication.Name != parent.Component {
				continue
			}
			path := "api/v1/crates/" + url.PathEscape(publication.Name) + "/" + url.PathEscape(publication.Version) + "/download"
			nodes[path] = ArtifactBrowseNode{
				Key: path, Kind: BrowseNodeAsset, Name: publication.Name + "-" + publication.Version + ".crate",
				Path: path, Coordinate: parent.Version, Digest: publication.Digest,
				Size: publication.Size, ContentType: "application/octet-stream", CreatedAt: publication.PublishedAt,
			}
		}
	}
	return browseNodeMapValues(nodes)
}

func cargoBrowseVersionCoordinate(coordinate string) (string, string, bool) {
	name, version, ok := strings.Cut(coordinate, "@")
	if !ok {
		return "", "", false
	}
	_, err := cargo.NormalizeIdentity(name, version)
	return name, version, err == nil
}
