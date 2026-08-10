package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	Digest      string
}

type ArtifactSearchMode string

const (
	ArtifactSearchByCoordinate ArtifactSearchMode = "coordinate"
	ArtifactSearchByDigest     ArtifactSearchMode = "digest"
)

type ArtifactSearchQuery struct {
	Mode  ArtifactSearchMode
	Value string
}

type ArtifactSearchItem struct {
	Coordinate   string
	Version      string
	Digest       string
	CreatedAt    *time.Time
	Size         *int64
	Publisher    string
	BuildNumber  int
	ContentType  string
	Intelligence *ArtifactIntelligenceSummary
}

// ArtifactSearchStore exposes the format-neutral management projection. Both
// PostgreSQL and memory mode implement it so search semantics stay aligned.
type ArtifactSearchStore interface {
	SearchArtifactProjection(context.Context, string, Format, ArtifactSearchQuery, int, ArtifactSearchPosition) ([]ArtifactSearchItem, error)
}

func (s *PostgresStore) SearchArtifactProjection(ctx context.Context, repositoryID string, format Format, query ArtifactSearchQuery, limit int, after ArtifactSearchPosition) ([]ArtifactSearchItem, error) {
	if limit <= 0 {
		limit = 100
	}
	if query.Mode != ArtifactSearchByCoordinate && query.Mode != ArtifactSearchByDigest {
		return nil, fmt.Errorf("unsupported artifact search mode %q", query.Mode)
	}
	value := query.Value
	if query.Mode == ArtifactSearchByCoordinate {
		value = escapeLikePrefix(value)
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH ranked AS (
			SELECT coordinate,digest,created_at,size,publisher,build_number,content_type,COALESCE(version,'') AS version,
			       row_number() OVER (PARTITION BY coordinate ORDER BY created_at DESC NULLS LAST,digest DESC) AS coordinate_rank,
			       row_number() OVER (PARTITION BY coordinate,digest ORDER BY created_at DESC NULLS LAST) AS digest_rank
			FROM artifact_search_projection
			WHERE repository_id::text=$1 AND format=$2
			  AND (($3='coordinate' AND ($4='' OR coordinate LIKE $4 || '%' ESCAPE '\'))
			       OR ($3='digest' AND digest=$4))
		)
		SELECT coordinate,COALESCE(digest,''),created_at,size,COALESCE(publisher,''),build_number,COALESCE(content_type,''),version,
		       CASE WHEN ai.repository_id IS NULL THEN NULL ELSE jsonb_build_object(
		         'signatureCount', jsonb_array_length(ai.signatures),
		         'sbomCount', jsonb_array_length(ai.sboms),
		         'licenseCount', jsonb_array_length(ai.licenses),
		         'vulnerabilityStatus', ai.vulnerability->>'status',
		         'critical', COALESCE((ai.vulnerability->>'critical')::int, 0),
		         'high', COALESCE((ai.vulnerability->>'high')::int, 0),
		         'medium', COALESCE((ai.vulnerability->>'medium')::int, 0),
		         'low', COALESCE((ai.vulnerability->>'low')::int, 0),
		         'unknown', COALESCE((ai.vulnerability->>'unknown')::int, 0)
		       ) END AS intelligence
		FROM ranked
		LEFT JOIN artifact_intelligence ai
		  ON ai.repository_id::text=$1 AND ai.format=$2 AND ai.coordinate=ranked.coordinate AND ai.digest=ranked.digest
		WHERE (($3='coordinate' AND ($2='maven' OR coordinate_rank=1))
		       OR ($3='digest' AND ($2='maven' OR digest_rank=1)))
		  AND (coordinate>$5
		       OR (coordinate=$5 AND build_number>$6)
		       OR ($7<>'' AND coordinate=$5 AND build_number=$6 AND digest>$7))
		ORDER BY coordinate,build_number,digest
		LIMIT $8`, repositoryID, format, query.Mode, value, after.Coordinate, after.BuildNumber, after.Digest, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]ArtifactSearchItem, 0, limit)
	for rows.Next() {
		var item ArtifactSearchItem
		var createdAt sql.NullTime
		var size sql.NullInt64
		var intelligence []byte
		if err := rows.Scan(&item.Coordinate, &item.Digest, &createdAt, &size, &item.Publisher, &item.BuildNumber, &item.ContentType, &item.Version, &intelligence); err != nil {
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
		if len(intelligence) > 0 && string(intelligence) != "null" {
			item.Intelligence = &ArtifactIntelligenceSummary{}
			if err := json.Unmarshal(intelligence, item.Intelligence); err != nil {
				return nil, err
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
