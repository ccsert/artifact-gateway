package repository

import (
	"context"
	"database/sql"
	"errors"
)

const repositoryCapacityRecordsQuery = `WITH usage AS (
	SELECT a.repository_id,COALESCE(SUM(o.size),0)::bigint AS used_bytes,COUNT(*)::bigint AS object_count
	FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest GROUP BY a.repository_id
	UNION ALL
	SELECT a.repository_id,COALESCE(SUM(a.size),0)::bigint,COUNT(*)::bigint
	FROM native_maven_assets a
	WHERE EXISTS (
		SELECT 1 FROM native_maven_artifacts m
		WHERE m.repository_id=a.repository_id AND m.state='visible'
		AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/')) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/'
	) GROUP BY a.repository_id
	UNION ALL
	SELECT repository_id,COALESCE(SUM(size),0)::bigint,COUNT(*)::bigint FROM (
		SELECT repository_id,size FROM native_oci_manifests
		UNION ALL
		SELECT rb.repository_id,b.size FROM native_oci_repository_blobs rb JOIN native_oci_blobs b ON b.digest=rb.digest
	) oci_objects GROUP BY repository_id
	UNION ALL
	SELECT a.repository_id,COALESCE(SUM(a.size),0)::bigint,COUNT(*)::bigint
	FROM native_conan_assets a
	JOIN native_conan_recipe_revisions r ON r.repository_id=a.repository_id AND r.reference=a.reference AND r.revision=a.recipe_revision
	LEFT JOIN native_conan_package_revisions p ON p.repository_id=a.repository_id AND p.reference=a.reference AND p.recipe_revision=a.recipe_revision AND p.package_id=a.package_id AND p.revision=a.package_revision
	WHERE r.state='visible' AND (a.package_id='' OR p.state='visible') GROUP BY a.repository_id
	UNION ALL
	SELECT repository_id,COALESCE(SUM(size),0)::bigint,COUNT(*)::bigint
	FROM native_npm_versions WHERE object_key<>'' GROUP BY repository_id
	UNION ALL
	SELECT repository_id,COALESCE(SUM(size),0)::bigint,COUNT(*)::bigint
	FROM native_pypi_files WHERE object_key<>'' AND state='visible' GROUP BY repository_id
	UNION ALL
	SELECT repository_id,COALESCE(SUM(size),0)::bigint,COUNT(*)::bigint
	FROM native_go_assets GROUP BY repository_id
	UNION ALL
	SELECT repository_id,COALESCE(SUM(size),0)::bigint,COUNT(*)::bigint
	FROM native_apt_assets GROUP BY repository_id
	UNION ALL
	SELECT repository_id,COALESCE(SUM(size),0)::bigint,COUNT(*)::bigint
	FROM native_apt_package_revisions GROUP BY repository_id
), totals AS (
	SELECT repository_id,SUM(used_bytes)::bigint AS used_bytes,SUM(object_count)::bigint AS object_count
	FROM usage GROUP BY repository_id
)
SELECT h.id::text,h.name,h.format,h.repo_type,h.endpoint,
	COALESCE(q.quota_bytes,0),COALESCE(t.used_bytes,0),COALESCE(t.object_count,0)
FROM hosted_repositories h
LEFT JOIN repository_capacity_quotas q ON q.repository_id=h.id
LEFT JOIN totals t ON t.repository_id=h.id
ORDER BY h.id`

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
		FormatNPM:   `SELECT COALESCE(SUM(size),0),COUNT(*) FROM native_npm_versions WHERE repository_id::text=$1 AND object_key<>''`,
		FormatPyPI:  `SELECT COALESCE(SUM(size),0),COUNT(*) FROM native_pypi_files WHERE repository_id::text=$1 AND object_key<>'' AND state='visible'`,
		FormatGo:    `SELECT COALESCE(SUM(size),0),COUNT(*) FROM native_go_assets WHERE repository_id::text=$1`,
		FormatAPT:   `SELECT COALESCE((SELECT SUM(size) FROM native_apt_assets WHERE repository_id::text=$1),0)+COALESCE((SELECT SUM(size) FROM native_apt_package_revisions WHERE repository_id::text=$1),0), (SELECT COUNT(*) FROM native_apt_assets WHERE repository_id::text=$1)+(SELECT COUNT(*) FROM native_apt_package_revisions WHERE repository_id::text=$1)`,
	}[capacity.Format]
	if err = s.db.QueryRowContext(ctx, query, id).Scan(&capacity.UsedBytes, &capacity.ObjectCount); err != nil {
		return RepositoryCapacity{}, err
	}
	return capacity, nil
}

func (s *PostgresStore) ListRepositoryCapacityRecords(ctx context.Context) ([]RepositoryCapacityRecord, error) {
	rows, err := s.db.QueryContext(ctx, repositoryCapacityRecordsQuery)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := []RepositoryCapacityRecord{}
	for rows.Next() {
		var record RepositoryCapacityRecord
		if err := rows.Scan(
			&record.Repository.ID,
			&record.Repository.Name,
			&record.Repository.Format,
			&record.Repository.Type,
			&record.Repository.Endpoint,
			&record.Capacity.QuotaBytes,
			&record.Capacity.UsedBytes,
			&record.Capacity.ObjectCount,
		); err != nil {
			return nil, err
		}
		record.Capacity.RepositoryID = record.Repository.ID
		record.Capacity.Format = record.Repository.Format
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresStore) ReplaceRepositoryCapacityQuota(ctx context.Context, id string, quota int64) (RepositoryCapacity, error) {
	if quota < 0 {
		return RepositoryCapacity{}, ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RepositoryCapacity{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var repositoryID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM hosted_repositories WHERE id::text=$1 FOR UPDATE`, id).Scan(&repositoryID); errors.Is(err, sql.ErrNoRows) {
		return RepositoryCapacity{}, ErrNotFound
	} else if err != nil {
		return RepositoryCapacity{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO repository_capacity_quotas(repository_id,quota_bytes) VALUES ($1,$2) ON CONFLICT(repository_id) DO UPDATE SET quota_bytes=EXCLUDED.quota_bytes`, repositoryID, quota)
	if err != nil {
		return RepositoryCapacity{}, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return RepositoryCapacity{}, ErrNotFound
	}
	if err = tx.Commit(); err != nil {
		return RepositoryCapacity{}, err
	}
	return s.GetRepositoryCapacity(ctx, id)
}
