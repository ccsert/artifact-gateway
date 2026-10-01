package repository

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func (s *PostgresStore) listPostgresCargoBrowseNodes(ctx context.Context, repositoryID string, parent ArtifactBrowseParent, limit int, after string) ([]ArtifactBrowseNode, error) {
	switch parent.Kind {
	case "":
		rows, err := s.db.QueryContext(ctx, `SELECT n.collision_key,n.name FROM native_cargo_names n
			WHERE n.repository_id::text=$1 AND n.collision_key>$2
			AND EXISTS (SELECT 1 FROM native_cargo_publications p
				JOIN native_cargo_identity_reservations r USING (repository_id,collision_key,version_key)
				WHERE p.repository_id=n.repository_id AND p.collision_key=n.collision_key AND p.collected_at IS NULL
				  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=p.repository_id
				    AND t.format='cargo' AND t.coordinate=n.name || '@' || r.version))
			ORDER BY n.collision_key LIMIT $3`, repositoryID, after, limit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		items := make([]ArtifactBrowseNode, 0)
		for rows.Next() {
			var key, name string
			if err := rows.Scan(&key, &name); err != nil {
				return nil, err
			}
			items = append(items, ArtifactBrowseNode{Key: key, Kind: BrowseNodeComponent, Name: name,
				Component: name, Coordinate: name, HasChildren: true})
		}
		return items, rows.Err()
	case BrowseNodeComponent:
		identity, err := cargo.NormalizeIdentity(parent.Component, "0.0.0")
		if err != nil {
			return nil, ErrInvalidCargoIdentity
		}
		rows, err := s.db.QueryContext(ctx, `SELECT p.version_key,r.version,r.digest,p.published_at
			FROM native_cargo_publications p JOIN native_cargo_identity_reservations r
			USING (repository_id,collision_key,version_key)
			JOIN native_cargo_names n USING (repository_id,collision_key)
			WHERE p.repository_id::text=$1 AND p.collision_key=$2 AND p.version_key>$3 AND p.collected_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=p.repository_id
			    AND t.format='cargo' AND t.coordinate=n.name || '@' || r.version)
			ORDER BY p.version_key LIMIT $4`, repositoryID, identity.CollisionKey, after, limit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		items := make([]ArtifactBrowseNode, 0)
		for rows.Next() {
			var key, version, digest string
			var publishedAt time.Time
			if err := rows.Scan(&key, &version, &digest, &publishedAt); err != nil {
				return nil, err
			}
			coordinate := parent.Component + "@" + version
			items = append(items, ArtifactBrowseNode{Key: key, Kind: BrowseNodeVersion, Name: version,
				Component: parent.Component, Version: coordinate, Coordinate: coordinate,
				Digest: digest, CreatedAt: publishedAt, HasChildren: true})
		}
		return items, rows.Err()
	case BrowseNodeVersion:
		name, version, valid := cargoBrowseVersionCoordinate(parent.Version)
		if !valid || name != parent.Component {
			return nil, ErrNotFound
		}
		publication, err := s.GetCargoPublication(ctx, repositoryID, name, version)
		if errors.Is(err, ErrNotFound) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		path := "api/v1/crates/" + url.PathEscape(publication.Name) + "/" + url.PathEscape(publication.Version) + "/download"
		if path <= after {
			return nil, nil
		}
		return []ArtifactBrowseNode{{Key: path, Kind: BrowseNodeAsset,
			Name: publication.Name + "-" + publication.Version + ".crate", Path: path,
			Coordinate: publication.Name + "@" + publication.Version, Digest: publication.Digest,
			Size: publication.Size, ContentType: "application/octet-stream", CreatedAt: publication.PublishedAt}}, nil
	default:
		return nil, ErrUnsupportedBrowseFormat
	}
}
