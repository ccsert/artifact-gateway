package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *PostgresStore) LockGoObject(ctx context.Context, objectKey string) (func(), error) {
	_, release, err := s.LockArtifactObjectKeys(ctx, FormatGo, []string{objectKey})
	return release, err
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

func (s *PostgresStore) PublishGoModule(ctx context.Context, incoming GoModulePublication) (GoModuleVersion, bool, error) {
	publication, err := normalizeGoModulePublication(incoming, time.Now().UTC())
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	version := publication.Version
	var existing GoModuleVersion
	err = scanGoModuleVersion(tx.QueryRowContext(ctx, `SELECT `+goModuleVersionColumns+` FROM native_go_versions
		WHERE repository_id::text=$1 AND module_path=$2 AND version=$3 FOR UPDATE`, version.RepositoryID, version.Module, version.Version), &existing)
	if err == nil {
		var tombstoned bool
		if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM artifact_tombstones
			WHERE repository_id::text=$1 AND format='go' AND coordinate=$2)`, version.RepositoryID, goModuleCoordinate(version.Module, version.Version)).Scan(&tombstoned); queryErr != nil {
			return GoModuleVersion{}, false, queryErr
		}
		if tombstoned {
			return GoModuleVersion{}, false, ErrArtifactTombstoned
		}
		rows, queryErr := tx.QueryContext(ctx, `SELECT `+goModuleAssetColumns+` FROM native_go_assets
			WHERE repository_id::text=$1 AND module_path=$2 AND version=$3 ORDER BY kind`, version.RepositoryID, version.Module, version.Version)
		if queryErr != nil {
			return GoModuleVersion{}, false, queryErr
		}
		assets := make([]GoModuleAsset, 0, 3)
		for rows.Next() {
			var asset GoModuleAsset
			if queryErr = scanGoModuleAsset(rows, &asset); queryErr != nil {
				_ = rows.Close()
				return GoModuleVersion{}, false, queryErr
			}
			assets = append(assets, asset)
		}
		queryErr = errors.Join(rows.Err(), rows.Close())
		if queryErr != nil {
			return GoModuleVersion{}, false, queryErr
		}
		if !goModulePublicationMatches(assets, publication.Assets) {
			return GoModuleVersion{}, false, ErrNameExists
		}
		if err = tx.Commit(); err != nil {
			return GoModuleVersion{}, false, err
		}
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return GoModuleVersion{}, false, err
	}
	err = scanGoModuleVersion(tx.QueryRowContext(ctx, `INSERT INTO native_go_versions
		(repository_id,module_path,version,published_at,publisher,cached_at,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING `+goModuleVersionColumns,
		version.RepositoryID, version.Module, version.Version, version.PublishedAt, version.Publisher, version.CachedAt, version.CreatedAt), &version)
	if isUnique(err) {
		return GoModuleVersion{}, false, ErrNameExists
	}
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	for _, asset := range publication.Assets {
		_, err = tx.ExecContext(ctx, `INSERT INTO native_go_assets
			(repository_id,module_path,version,kind,digest,object_key,size,source_url,cached_at,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			asset.RepositoryID, asset.Module, asset.Version, asset.Kind, asset.Digest, asset.ObjectKey, asset.Size, asset.SourceURL, asset.CachedAt, asset.CreatedAt)
		if IsQuotaExceeded(err) {
			return GoModuleVersion{}, false, ErrQuotaExceeded
		}
		if isUnique(err) {
			return GoModuleVersion{}, false, ErrNameExists
		}
		if err != nil {
			return GoModuleVersion{}, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return GoModuleVersion{}, false, err
	}
	return version, false, nil
}

func (s *PostgresStore) ListGoModuleVersions(ctx context.Context, repositoryID, modulePath string) ([]GoModuleVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixedGoModuleVersionColumns("v")+` FROM native_go_versions v
		WHERE v.repository_id::text=$1 AND v.module_path=$2
		  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=v.repository_id AND t.format='go' AND t.coordinate=v.module_path || '@' || v.version)
		ORDER BY v.version`, repositoryID, modulePath)
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
	err := scanGoModuleVersion(s.db.QueryRowContext(ctx, `SELECT `+prefixedGoModuleVersionColumns("v")+` FROM native_go_versions v
		WHERE v.repository_id::text=$1 AND v.module_path=$2 AND v.version=$3
		  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=v.repository_id AND t.format='go' AND t.coordinate=v.module_path || '@' || v.version)`, repositoryID, modulePath, version), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return GoModuleVersion{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) TombstoneGoModuleVersion(ctx context.Context, repositoryID, modulePath, version string) (GoModuleVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoModuleVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var item GoModuleVersion
	err = scanGoModuleVersion(tx.QueryRowContext(ctx, `SELECT `+prefixedGoModuleVersionColumns("v")+` FROM native_go_versions v
		WHERE v.repository_id::text=$1 AND v.module_path=$2 AND v.version=$3
		  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=v.repository_id AND t.format='go' AND t.coordinate=v.module_path || '@' || v.version)
		FOR UPDATE OF v`, repositoryID, modulePath, version), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return GoModuleVersion{}, ErrNotFound
	}
	if err != nil {
		return GoModuleVersion{}, err
	}
	var digest string
	err = tx.QueryRowContext(ctx, `SELECT digest FROM native_go_assets
		WHERE repository_id::text=$1 AND module_path=$2 AND version=$3
		ORDER BY CASE kind WHEN 'zip' THEN 0 WHEN 'mod' THEN 1 ELSE 2 END LIMIT 1`, repositoryID, modulePath, version).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		digest = ""
	} else if err != nil {
		return GoModuleVersion{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO artifact_tombstones (repository_id,format,coordinate,digest)
		VALUES ($1,'go',$2,$3) ON CONFLICT (repository_id,format,coordinate) DO NOTHING`, repositoryID, goModuleCoordinate(modulePath, version), digest)
	if err != nil {
		return GoModuleVersion{}, err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
		return GoModuleVersion{}, rowsErr
	} else if affected != 1 {
		return GoModuleVersion{}, ErrNotFound
	}
	if err = tx.Commit(); err != nil {
		return GoModuleVersion{}, err
	}
	return item, nil
}

func (s *PostgresStore) RestoreGoModuleVersion(ctx context.Context, repositoryID, modulePath, version string) (GoModuleVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoModuleVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var item GoModuleVersion
	err = scanGoModuleVersion(tx.QueryRowContext(ctx, `SELECT `+prefixedGoModuleVersionColumns("v")+` FROM native_go_versions v
		JOIN artifact_tombstones t ON t.repository_id=v.repository_id AND t.format='go' AND t.coordinate=v.module_path || '@' || v.version
		WHERE v.repository_id::text=$1 AND v.module_path=$2 AND v.version=$3
		FOR UPDATE OF v,t`, repositoryID, modulePath, version), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return GoModuleVersion{}, ErrNotFound
	}
	if err != nil {
		return GoModuleVersion{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM artifact_tombstones
		WHERE repository_id::text=$1 AND format='go' AND coordinate=$2`, repositoryID, goModuleCoordinate(modulePath, version)); err != nil {
		return GoModuleVersion{}, err
	}
	if err = tx.Commit(); err != nil {
		return GoModuleVersion{}, err
	}
	return item, nil
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
	err := scanGoModuleAsset(s.db.QueryRowContext(ctx, `SELECT `+prefixedGoModuleAssetColumns("a")+` FROM native_go_assets a
		WHERE a.repository_id::text=$1 AND a.module_path=$2 AND a.version=$3 AND a.kind=$4
		  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=a.repository_id AND t.format='go' AND t.coordinate=a.module_path || '@' || a.version)`, repositoryID, modulePath, version, kind), &asset)
	if errors.Is(err, sql.ErrNoRows) {
		return GoModuleAsset{}, ErrNotFound
	}
	return asset, err
}

func (s *PostgresStore) GoModuleObjectHasReference(ctx context.Context, objectKey string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_go_assets WHERE object_key=$1)`, objectKey).Scan(&referenced)
	return referenced, err
}

const goModuleVersionColumns = `repository_id::text,module_path,version,published_at,publisher,cached_at,created_at`
const goModuleAssetColumns = `repository_id::text,module_path,version,kind,digest,object_key,size,source_url,cached_at,created_at`

func prefixedGoModuleVersionColumns(prefix string) string {
	return prefix + `.repository_id::text,` + prefix + `.module_path,` + prefix + `.version,` + prefix + `.published_at,` + prefix + `.publisher,` + prefix + `.cached_at,` + prefix + `.created_at`
}

func prefixedGoModuleAssetColumns(prefix string) string {
	return prefix + `.repository_id::text,` + prefix + `.module_path,` + prefix + `.version,` + prefix + `.kind,` + prefix + `.digest,` + prefix + `.object_key,` + prefix + `.size,` + prefix + `.source_url,` + prefix + `.cached_at,` + prefix + `.created_at`
}

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
