package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) GetRepositoryCapacity(ctx context.Context, id string) (RepositoryCapacity, error) {
	var capacity RepositoryCapacity
	err := s.db.QueryRowContext(ctx, `SELECT format,COALESCE((SELECT quota_bytes FROM repository_capacity_quotas WHERE repository_id=h.id),0) FROM hosted_repositories h WHERE id::text=$1`, id).Scan(&capacity.Format, &capacity.QuotaBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryCapacity{}, ErrNotFound
	}
	if err != nil {
		return RepositoryCapacity{}, err
	}
	capacity.RepositoryID = id
	query := map[Format]string{
		FormatRaw: `SELECT COALESCE(SUM(o.size),0),COUNT(*) FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest WHERE a.repository_id::text=$1`,
		FormatMaven: `SELECT COALESCE(SUM(a.size),0),COUNT(*)
            FROM native_maven_assets a
            WHERE a.repository_id::text=$1
              AND EXISTS (
                  SELECT 1 FROM native_maven_artifacts m
                  WHERE m.repository_id=a.repository_id AND m.state='visible'
                    AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/')) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/'
              )`,
		FormatOCI:   `SELECT COALESCE((SELECT SUM(size) FROM native_oci_manifests WHERE repository_id::text=$1),0)+COALESCE((SELECT SUM(b.size) FROM native_oci_repository_blobs rb JOIN native_oci_blobs b ON b.digest=rb.digest WHERE rb.repository_id::text=$1),0), (SELECT COUNT(*) FROM native_oci_manifests WHERE repository_id::text=$1)+(SELECT COUNT(*) FROM native_oci_repository_blobs WHERE repository_id::text=$1)`,
		FormatConan: `SELECT COALESCE(SUM(a.size),0),COUNT(*) FROM native_conan_assets a JOIN native_conan_recipe_revisions r ON r.repository_id=a.repository_id AND r.reference=a.reference AND r.revision=a.recipe_revision LEFT JOIN native_conan_package_revisions p ON p.repository_id=a.repository_id AND p.reference=a.reference AND p.recipe_revision=a.recipe_revision AND p.package_id=a.package_id AND p.revision=a.package_revision WHERE a.repository_id::text=$1 AND r.state='visible' AND (a.package_id='' OR p.state='visible')`,
	}[capacity.Format]
	if err = s.db.QueryRowContext(ctx, query, id).Scan(&capacity.UsedBytes, &capacity.ObjectCount); err != nil {
		return RepositoryCapacity{}, err
	}
	return capacity, nil
}

func (s *PostgresStore) ReplaceRepositoryCapacityQuota(ctx context.Context, id string, quota int64) (RepositoryCapacity, error) {
	if quota < 0 {
		return RepositoryCapacity{}, ErrDisabled
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO repository_capacity_quotas(repository_id,quota_bytes) SELECT id,$2 FROM hosted_repositories WHERE id::text=$1 ON CONFLICT(repository_id) DO UPDATE SET quota_bytes=EXCLUDED.quota_bytes`, id, quota)
	if err != nil {
		return RepositoryCapacity{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return RepositoryCapacity{}, ErrNotFound
	}
	return s.GetRepositoryCapacity(ctx, id)
}
