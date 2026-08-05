package repository

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func escapeLikePrefix(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// ArtifactSearchPosition is shared by all format projections. BuildNumber is
// non-zero only for Maven SNAPSHOT builds.
type ArtifactSearchPosition struct {
	Coordinate  string
	BuildNumber int
}

type ArtifactSearchItem struct {
	Coordinate  string
	Digest      string
	CreatedAt   *time.Time
	Size        *int64
	Publisher   string
	BuildNumber int
	ContentType string
}

// ArtifactSearchStore exposes the format-neutral PostgreSQL projection. The
// interface is optional so MemoryStore and lightweight tests can keep their
// existing format-specific implementation.
type ArtifactSearchStore interface {
	SearchArtifactProjection(context.Context, string, Format, string, int, ArtifactSearchPosition) ([]ArtifactSearchItem, error)
}

func (s *PostgresStore) SearchArtifactProjection(ctx context.Context, repositoryID string, format Format, prefix string, limit int, after ArtifactSearchPosition) ([]ArtifactSearchItem, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT coordinate,COALESCE(digest,''),created_at,size,COALESCE(publisher,''),build_number,COALESCE(content_type,'')
		FROM artifact_search_projection
		WHERE repository_id::text=$1 AND format=$2 AND ($3='' OR coordinate LIKE $3 || '%' ESCAPE '\')
		  AND (coordinate>$4 OR (coordinate=$4 AND build_number>$5))
		ORDER BY coordinate,build_number
		LIMIT $6`, repositoryID, format, escapeLikePrefix(prefix), after.Coordinate, after.BuildNumber, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]ArtifactSearchItem, 0, limit)
	for rows.Next() {
		var item ArtifactSearchItem
		var createdAt sql.NullTime
		var size sql.NullInt64
		if err := rows.Scan(&item.Coordinate, &item.Digest, &createdAt, &size, &item.Publisher, &item.BuildNumber, &item.ContentType); err != nil {
			return nil, err
		}
		if createdAt.Valid {
			value := createdAt.Time
			item.CreatedAt = &value
		}
		if size.Valid {
			value := size.Int64
			item.Size = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
