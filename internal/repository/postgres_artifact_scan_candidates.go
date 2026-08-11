package repository

import (
	"context"
	"fmt"
)

func (s *PostgresStore) ListArtifactScanCandidates(ctx context.Context, repositoryID string, format Format, limit int) ([]ArtifactScanCandidate, error) {
	limit = artifactScanCandidateLimit(limit)
	query := ""
	switch format {
	case FormatMaven:
		query = `SELECT coordinate,digest,created_at AS published_at FROM native_maven_artifacts WHERE repository_id::text=$1 AND state='visible'`
	case FormatOCI:
		query = `SELECT name AS coordinate,digest,created_at AS published_at FROM native_oci_manifests WHERE repository_id::text=$1`
	case FormatRaw:
		query = `SELECT path AS coordinate,digest,updated_at AS published_at FROM native_raw_assets WHERE repository_id::text=$1`
	case FormatNPM:
		query = `SELECT v.package_name || '@' || v.version AS coordinate,v.digest,v.created_at AS published_at FROM native_npm_versions v JOIN native_npm_packages p ON p.repository_id=v.repository_id AND p.name=v.package_name WHERE v.repository_id::text=$1 AND v.state='visible' AND NOT p.negative`
	case FormatPyPI:
		query = `SELECT project || '@' || version AS coordinate,digest,created_at AS published_at FROM native_pypi_files WHERE repository_id::text=$1 AND state='visible'`
	case FormatConan:
		query = `SELECT reference || '#' || revision AS coordinate,digest,created_at AS published_at FROM native_conan_recipe_revisions WHERE repository_id::text=$1 AND state='visible'
			UNION ALL SELECT p.reference || '#' || p.recipe_revision || '/' || p.package_id || '#' || p.revision AS coordinate,p.digest,p.created_at AS published_at FROM native_conan_package_revisions p JOIN native_conan_recipe_revisions r ON r.repository_id=p.repository_id AND r.reference=p.reference AND r.revision=p.recipe_revision AND r.state='visible' WHERE p.repository_id::text=$1 AND p.state='visible'`
	default:
		return nil, fmt.Errorf("format %q does not support publication scan reconciliation", format)
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
