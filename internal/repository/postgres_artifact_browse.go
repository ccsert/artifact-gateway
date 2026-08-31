package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (s *PostgresStore) ListArtifactBrowseNodes(ctx context.Context, repositoryID string, format Format, parent ArtifactBrowseParent, limit int, after string) ([]ArtifactBrowseNode, error) {
	if limit <= 0 {
		limit = 50
	}
	switch format {
	case FormatMaven:
		return s.listPostgresMavenBrowseNodes(ctx, repositoryID, parent, limit, after)
	case FormatRaw:
		return s.listPostgresRawBrowseNodes(ctx, repositoryID, parent, limit, after)
	default:
		return nil, ErrUnsupportedBrowseFormat
	}
}

func (s *PostgresStore) listPostgresMavenBrowseNodes(ctx context.Context, repositoryID string, parent ArtifactBrowseParent, limit int, after string) ([]ArtifactBrowseNode, error) {
	switch parent.Kind {
	case "":
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT split_part(coordinate, ':', 1) AS namespace
			FROM native_maven_artifacts
			WHERE repository_id=$1::uuid AND state='visible' AND split_part(coordinate, ':', 3)<>''
				AND split_part(coordinate, ':', 1)>$2
			ORDER BY namespace LIMIT $3`, repositoryID, after, limit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		items := make([]ArtifactBrowseNode, 0)
		for rows.Next() {
			var namespace string
			if err := rows.Scan(&namespace); err != nil {
				return nil, err
			}
			items = append(items, ArtifactBrowseNode{Key: namespace, Kind: BrowseNodeNamespace, Name: namespace, HasChildren: true, Namespace: namespace})
		}
		return items, rows.Err()
	case BrowseNodeNamespace:
		rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT split_part(coordinate, ':', 2) AS component
			FROM native_maven_artifacts
			WHERE repository_id=$1::uuid AND state='visible' AND split_part(coordinate, ':', 1)=$2
				AND split_part(coordinate, ':', 3)<>'' AND split_part(coordinate, ':', 2)>$3
			ORDER BY component LIMIT $4`, repositoryID, parent.Namespace, after, limit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		items := make([]ArtifactBrowseNode, 0)
		for rows.Next() {
			var component string
			if err := rows.Scan(&component); err != nil {
				return nil, err
			}
			items = append(items, ArtifactBrowseNode{Key: component, Kind: BrowseNodeComponent, Name: component, HasChildren: true, Namespace: parent.Namespace, Component: component})
		}
		return items, rows.Err()
	case BrowseNodeComponent:
		rows, err := s.db.QueryContext(ctx, `SELECT coordinate,digest,created_at,build_number FROM (
				SELECT coordinate,digest,created_at,build_number,
					row_number() OVER (PARTITION BY coordinate ORDER BY build_number DESC, created_at DESC) AS browse_rank
				FROM native_maven_artifacts
				WHERE repository_id=$1::uuid AND state='visible'
					AND split_part(coordinate, ':', 1)=$2 AND split_part(coordinate, ':', 2)=$3
			) versions
			WHERE browse_rank=1 AND coordinate>$4
			ORDER BY coordinate LIMIT $5`, repositoryID, parent.Namespace, parent.Component, after, limit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		items := make([]ArtifactBrowseNode, 0)
		for rows.Next() {
			var coordinate, digest string
			var buildNumber int
			var createdAt time.Time
			if err := rows.Scan(&coordinate, &digest, &createdAt, &buildNumber); err != nil {
				return nil, err
			}
			parts := strings.Split(coordinate, ":")
			if len(parts) < 3 {
				continue
			}
			items = append(items, ArtifactBrowseNode{Key: coordinate, Kind: BrowseNodeVersion, Name: parts[2], HasChildren: true, Namespace: parent.Namespace, Component: parent.Component, Version: parts[2], Coordinate: coordinate, BuildNumber: buildNumber, Digest: digest, CreatedAt: createdAt})
		}
		return items, rows.Err()
	case BrowseNodeVersion:
		var createdAt time.Time
		err := s.db.QueryRowContext(ctx, `SELECT created_at FROM native_maven_artifacts
			WHERE repository_id=$1::uuid AND coordinate=$2 AND build_number=$3 AND state='visible'`, repositoryID, parent.Version, parent.BuildNumber).Scan(&createdAt)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		prefix := mavenArtifactPathPrefix(parent.Version)
		if parent.BuildNumber > 0 {
			prefix += mavenSnapshotBuildFilePrefix(parent.Version, createdAt, parent.BuildNumber)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT path,digest,size FROM native_maven_assets
			WHERE repository_id=$1::uuid AND left(path,length($2))=$2 AND path>$3
			ORDER BY path LIMIT $4`, repositoryID, prefix, after, limit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		items := make([]ArtifactBrowseNode, 0)
		for rows.Next() {
			var path, digest string
			var size int64
			if err := rows.Scan(&path, &digest, &size); err != nil {
				return nil, err
			}
			name := path
			if slash := strings.LastIndex(name, "/"); slash >= 0 {
				name = name[slash+1:]
			}
			items = append(items, ArtifactBrowseNode{Key: path, Kind: BrowseNodeAsset, Name: name, Path: path, Coordinate: parent.Version, Digest: digest, Size: size})
		}
		return items, rows.Err()
	default:
		return nil, ErrUnsupportedBrowseFormat
	}
}

func (s *PostgresStore) listPostgresRawBrowseNodes(ctx context.Context, repositoryID string, parent ArtifactBrowseParent, limit int, after string) ([]ArtifactBrowseNode, error) {
	prefix := parent.Path
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
			SELECT a.path,a.digest,o.size,a.content_type,a.updated_at,
				substring(a.path FROM char_length($2) + 1) AS remainder
			FROM native_raw_assets a
			JOIN native_raw_objects o ON o.digest=a.digest
			WHERE a.repository_id=$1::uuid AND a.path LIKE $3 || '%' ESCAPE '\'
		), direct AS (
			SELECT split_part(remainder, '/', 1) || chr(31) || '0' AS sort_key,
				split_part(remainder, '/', 1) AS name,true AS has_children,
				NULL::text AS path,NULL::text AS digest,NULL::bigint AS size,
				NULL::text AS content_type,NULL::timestamptz AS created_at
			FROM candidates WHERE position('/' IN remainder)>0
			GROUP BY split_part(remainder, '/', 1)
			UNION ALL
			SELECT remainder || chr(31) || '1',remainder,false,path,digest,size,content_type,updated_at
			FROM candidates WHERE position('/' IN remainder)=0 AND remainder<>''
		)
		SELECT sort_key,name,has_children,path,digest,size,content_type,created_at
		FROM direct WHERE sort_key>$4 ORDER BY sort_key LIMIT $5`, repositoryID, prefix, escapeLikePrefix(prefix), after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]ArtifactBrowseNode, 0)
	for rows.Next() {
		var key, name string
		var hasChildren bool
		var path, digest, contentType *string
		var size *int64
		var createdAt *time.Time
		if err := rows.Scan(&key, &name, &hasChildren, &path, &digest, &size, &contentType, &createdAt); err != nil {
			return nil, err
		}
		node := ArtifactBrowseNode{Key: key, Name: name, HasChildren: hasChildren}
		if hasChildren {
			node.Kind = BrowseNodeDirectory
			node.Path = prefix + name
		} else {
			node.Kind = BrowseNodeAsset
			node.Path, node.Coordinate = *path, *path
			node.Digest, node.Size, node.ContentType, node.CreatedAt = *digest, *size, *contentType, *createdAt
		}
		items = append(items, node)
	}
	return items, rows.Err()
}
