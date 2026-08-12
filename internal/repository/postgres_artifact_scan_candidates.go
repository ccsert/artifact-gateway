package repository

import (
	"context"
	"fmt"
)

func (s *PostgresStore) ListArtifactScanCandidates(ctx context.Context, repositoryID string, format Format, limit int) ([]ArtifactScanCandidate, error) {
	limit = artifactScanCandidateLimit(limit)
	if !formatSupportsPublicationScanReconciliation(format) {
		return nil, fmt.Errorf("format %q does not support publication scan reconciliation", format)
	}
	query, err := postgresArtifactIdentityQuery(format, ArtifactIdentityScan)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT candidates.coordinate,candidates.digest,candidates.published_at FROM (`+query+`) candidates
		LEFT JOIN LATERAL (
			SELECT state FROM lifecycle_jobs
			WHERE repository_id::text=$1 AND kind='scan'
			  AND payload->>'format'=$3 AND payload->>'coordinate'=candidates.coordinate AND payload->>'digest'=candidates.digest
			ORDER BY created_at DESC,id DESC LIMIT 1
		) latest_scan ON true
		ORDER BY CASE WHEN latest_scan.state IS NULL OR latest_scan.state IN ('failed','cancelled') THEN 0 ELSE 1 END,
			candidates.published_at DESC,candidates.coordinate,candidates.digest DESC LIMIT $2`, repositoryID, limit, format)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	candidates := make([]ArtifactScanCandidate, 0, limit)
	for rows.Next() {
		var candidate ArtifactScanCandidate
		if err := rows.Scan(&candidate.Coordinate, &candidate.Digest, &candidate.PublishedAt); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}
