package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *PostgresStore) LockGoObject(ctx context.Context, objectKey string) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	lockKey := "native-go-object:" + objectKey
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockKey)
		_ = conn.Close()
	}, nil
}

func (s *PostgresStore) SyncGoProxyVersions(ctx context.Context, repositoryID, modulePath string, versions []GoModuleVersion) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, version := range versions {
		_, err = tx.ExecContext(ctx, `INSERT INTO native_go_versions
			(repository_id,module_path,version,published_at,publisher,cached_at)
			VALUES ($1,$2,$3,$4,$5,now())
			ON CONFLICT (repository_id,module_path,version) DO UPDATE SET
			published_at=COALESCE(EXCLUDED.published_at,native_go_versions.published_at),
			publisher=CASE WHEN EXCLUDED.publisher='' THEN native_go_versions.publisher ELSE EXCLUDED.publisher END,
			cached_at=now()`, repositoryID, modulePath, version.Version, nullableGoTime(version.PublishedAt), version.Publisher)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) PutGoModuleVersion(ctx context.Context, version GoModuleVersion) (GoModuleVersion, error) {
	err := scanGoModuleVersion(s.db.QueryRowContext(ctx, `INSERT INTO native_go_versions
		(repository_id,module_path,version,published_at,publisher,cached_at)
		VALUES ($1,$2,$3,$4,$5,now())
		ON CONFLICT (repository_id,module_path,version) DO UPDATE SET
		published_at=COALESCE(EXCLUDED.published_at,native_go_versions.published_at),
		publisher=CASE WHEN EXCLUDED.publisher='' THEN native_go_versions.publisher ELSE EXCLUDED.publisher END,
		cached_at=now()
		RETURNING `+goModuleVersionColumns,
		version.RepositoryID, version.Module, version.Version, nullableGoTime(version.PublishedAt), version.Publisher), &version)
	return version, err
}

func (s *PostgresStore) ListGoModuleVersions(ctx context.Context, repositoryID, modulePath string) ([]GoModuleVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+goModuleVersionColumns+` FROM native_go_versions
		WHERE repository_id::text=$1 AND module_path=$2 ORDER BY version`, repositoryID, modulePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]GoModuleVersion, 0)
	for rows.Next() {
		var version GoModuleVersion
		if err = scanGoModuleVersion(rows, &version); err != nil {
			return nil, err
		}
		items = append(items, version)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items, nil
}

func (s *PostgresStore) GetGoModuleVersion(ctx context.Context, repositoryID, modulePath, version string) (GoModuleVersion, error) {
	var item GoModuleVersion
	err := scanGoModuleVersion(s.db.QueryRowContext(ctx, `SELECT `+goModuleVersionColumns+` FROM native_go_versions
		WHERE repository_id::text=$1 AND module_path=$2 AND version=$3`, repositoryID, modulePath, version), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return GoModuleVersion{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) CacheGoModuleAsset(ctx context.Context, asset GoModuleAsset) (GoModuleAsset, error) {
	incoming := asset
	var stored GoModuleAsset
	err := scanGoModuleAsset(s.db.QueryRowContext(ctx, `INSERT INTO native_go_assets
		(repository_id,module_path,version,kind,digest,object_key,size,source_url,cached_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())
		ON CONFLICT (repository_id,module_path,version,kind) DO UPDATE SET cached_at=native_go_assets.cached_at
		RETURNING `+goModuleAssetColumns,
		incoming.RepositoryID, incoming.Module, incoming.Version, incoming.Kind, incoming.Digest, incoming.ObjectKey, incoming.Size, incoming.SourceURL), &stored)
	if isUnique(err) {
		return GoModuleAsset{}, ErrUpstreamChanged
	}
	if IsQuotaExceeded(err) {
		return GoModuleAsset{}, ErrQuotaExceeded
	}
	if err != nil {
		return GoModuleAsset{}, err
	}
	if stored.Digest != incoming.Digest || stored.ObjectKey != incoming.ObjectKey || stored.Size != incoming.Size || stored.SourceURL != incoming.SourceURL {
		return GoModuleAsset{}, ErrUpstreamChanged
	}
	return stored, nil
}

func (s *PostgresStore) GetGoModuleAsset(ctx context.Context, repositoryID, modulePath, version, kind string) (GoModuleAsset, error) {
	var asset GoModuleAsset
	err := scanGoModuleAsset(s.db.QueryRowContext(ctx, `SELECT `+goModuleAssetColumns+` FROM native_go_assets
		WHERE repository_id::text=$1 AND module_path=$2 AND version=$3 AND kind=$4`, repositoryID, modulePath, version, kind), &asset)
	if errors.Is(err, sql.ErrNoRows) {
		return GoModuleAsset{}, ErrNotFound
	}
	return asset, err
}

const goModuleVersionColumns = `repository_id::text,module_path,version,published_at,publisher,cached_at,created_at`
const goModuleAssetColumns = `repository_id::text,module_path,version,kind,digest,object_key,size,source_url,cached_at,created_at`

func scanGoModuleVersion(row interface{ Scan(...any) error }, version *GoModuleVersion) error {
	var publishedAt sql.NullTime
	err := row.Scan(&version.RepositoryID, &version.Module, &version.Version, &publishedAt, &version.Publisher, &version.CachedAt, &version.CreatedAt)
	if publishedAt.Valid {
		version.PublishedAt = publishedAt.Time
	}
	return err
}

func scanGoModuleAsset(row interface{ Scan(...any) error }, asset *GoModuleAsset) error {
	return row.Scan(&asset.RepositoryID, &asset.Module, &asset.Version, &asset.Kind, &asset.Digest,
		&asset.ObjectKey, &asset.Size, &asset.SourceURL, &asset.CachedAt, &asset.CreatedAt)
}

func nullableGoTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
