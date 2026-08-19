package repository

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"sort"
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

func (s *PostgresStore) ListGoModules(ctx context.Context, repositoryID, prefix string, limit int, after string) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT v.module_path FROM native_go_versions v
		WHERE v.repository_id::text=$1 AND v.module_path LIKE $2 || '%' ESCAPE '\' AND v.module_path>$3
		  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=v.repository_id AND t.format='go' AND t.coordinate=v.module_path || '@' || v.version)
		ORDER BY v.module_path LIMIT $4`, repositoryID, escapeLikePrefix(prefix), after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	modules := make([]string, 0)
	for rows.Next() {
		var modulePath string
		if err = rows.Scan(&modulePath); err != nil {
			return nil, err
		}
		modules = append(modules, modulePath)
	}
	return modules, rows.Err()
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
	for attempt := 0; attempt < 3; attempt++ {
		objectKeys, err := s.goModuleObjectKeys(ctx, repositoryID, modulePath, version)
		if err != nil {
			return GoModuleVersion{}, err
		}
		objectCtx, releaseObjects, err := LockObjectKeys(ctx, objectKeys, s, FormatGo, s.LockGoObject)
		if err != nil {
			return GoModuleVersion{}, err
		}
		item, membershipChanged, tombstoneErr := s.tombstoneGoModuleVersionLocked(objectCtx, repositoryID, modulePath, version, objectKeys)
		releaseObjects()
		if membershipChanged {
			continue
		}
		return item, tombstoneErr
	}
	return GoModuleVersion{}, ErrUpstreamChanged
}

func (s *PostgresStore) goModuleObjectKeys(ctx context.Context, repositoryID, modulePath, version string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT object_key FROM native_go_assets
		WHERE repository_id::text=$1 AND module_path=$2 AND version=$3 ORDER BY object_key`, repositoryID, modulePath, version)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	keys := make([]string, 0, 3)
	for rows.Next() {
		var key string
		if err = rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *PostgresStore) tombstoneGoModuleVersionLocked(ctx context.Context, repositoryID, modulePath, version string, expectedObjectKeys []string) (GoModuleVersion, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var item GoModuleVersion
	err = scanGoModuleVersion(tx.QueryRowContext(ctx, `SELECT `+prefixedGoModuleVersionColumns("v")+` FROM native_go_versions v
		WHERE v.repository_id::text=$1 AND v.module_path=$2 AND v.version=$3
		  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=v.repository_id AND t.format='go' AND t.coordinate=v.module_path || '@' || v.version)
		FOR UPDATE OF v`, repositoryID, modulePath, version), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return GoModuleVersion{}, false, ErrNotFound
	}
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT object_key FROM native_go_assets
		WHERE repository_id::text=$1 AND module_path=$2 AND version=$3 ORDER BY object_key FOR UPDATE`, repositoryID, modulePath, version)
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	currentObjectKeys := make([]string, 0, 3)
	for rows.Next() {
		var objectKey string
		if err = rows.Scan(&objectKey); err != nil {
			_ = rows.Close()
			return GoModuleVersion{}, false, err
		}
		currentObjectKeys = append(currentObjectKeys, objectKey)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return GoModuleVersion{}, false, err
	}
	if len(currentObjectKeys) != 3 {
		return GoModuleVersion{}, false, ErrDisabled
	}
	if !slices.Equal(expectedObjectKeys, currentObjectKeys) {
		return GoModuleVersion{}, true, nil
	}
	var digest string
	err = tx.QueryRowContext(ctx, `SELECT digest FROM native_go_assets
		WHERE repository_id::text=$1 AND module_path=$2 AND version=$3
		ORDER BY CASE kind WHEN 'zip' THEN 0 WHEN 'mod' THEN 1 ELSE 2 END LIMIT 1`, repositoryID, modulePath, version).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		digest = ""
	} else if err != nil {
		return GoModuleVersion{}, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO artifact_tombstones (repository_id,format,coordinate,digest)
		VALUES ($1,'go',$2,$3) ON CONFLICT (repository_id,format,coordinate) DO NOTHING`, repositoryID, goModuleCoordinate(modulePath, version), digest)
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
		return GoModuleVersion{}, false, rowsErr
	} else if affected != 1 {
		return GoModuleVersion{}, false, ErrNotFound
	}
	if err = tx.Commit(); err != nil {
		return GoModuleVersion{}, false, err
	}
	return item, false, nil
}

func (s *PostgresStore) RestoreGoModuleVersion(ctx context.Context, repositoryID, modulePath, version string) (GoModuleVersion, error) {
	for attempt := 0; attempt < 3; attempt++ {
		objectKeys, tombstoned, complete, _, err := s.deletedGoObjectKeys(ctx, repositoryID, modulePath, version)
		if err != nil {
			return GoModuleVersion{}, err
		}
		if !tombstoned {
			return GoModuleVersion{}, ErrNotFound
		}
		if !complete {
			return GoModuleVersion{}, ErrDisabled
		}
		objectCtx, releaseObjects, err := LockObjectKeys(ctx, objectKeys, s, FormatGo, s.LockGoObject)
		if err != nil {
			return GoModuleVersion{}, err
		}
		item, membershipChanged, restoreErr := s.restoreGoModuleVersionLocked(objectCtx, repositoryID, modulePath, version, objectKeys)
		releaseObjects()
		if membershipChanged {
			continue
		}
		return item, restoreErr
	}
	return GoModuleVersion{}, ErrUpstreamChanged
}

func (s *PostgresStore) deletedGoObjectKeys(ctx context.Context, repositoryID, modulePath, version string) ([]string, bool, bool, bool, error) {
	var tombstoned bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_tombstones
		WHERE repository_id::text=$1 AND format='go' AND coordinate=$2)`, repositoryID, goModuleCoordinate(modulePath, version)).Scan(&tombstoned); err != nil || !tombstoned {
		return nil, tombstoned, false, false, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT object_key,(collecting_at IS NOT NULL OR collected_at IS NOT NULL)
		FROM native_go_assets WHERE repository_id::text=$1 AND module_path=$2 AND version=$3 ORDER BY object_key`, repositoryID, modulePath, version)
	if err != nil {
		return nil, true, false, false, err
	}
	defer func() { _ = rows.Close() }()
	objectKeys := make([]string, 0, 3)
	seen := make(map[string]bool)
	count, collected := 0, false
	for rows.Next() {
		var objectKey string
		var isCollected bool
		if err = rows.Scan(&objectKey, &isCollected); err != nil {
			return nil, true, false, false, err
		}
		count++
		collected = collected || isCollected
		if objectKey != "" && !seen[objectKey] {
			seen[objectKey] = true
			objectKeys = append(objectKeys, objectKey)
		}
	}
	return objectKeys, true, count == 3, collected, rows.Err()
}

func (s *PostgresStore) restoreGoModuleVersionLocked(ctx context.Context, repositoryID, modulePath, version string, expectedObjectKeys []string) (GoModuleVersion, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var item GoModuleVersion
	err = scanGoModuleVersion(tx.QueryRowContext(ctx, `SELECT `+prefixedGoModuleVersionColumns("v")+` FROM native_go_versions v
		JOIN artifact_tombstones t ON t.repository_id=v.repository_id AND t.format='go' AND t.coordinate=v.module_path || '@' || v.version
		WHERE v.repository_id::text=$1 AND v.module_path=$2 AND v.version=$3
		FOR UPDATE OF v,t`, repositoryID, modulePath, version), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return GoModuleVersion{}, false, ErrNotFound
	}
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT object_key,(collecting_at IS NOT NULL OR collected_at IS NOT NULL) FROM native_go_assets
		WHERE repository_id::text=$1 AND module_path=$2 AND version=$3 FOR UPDATE`, repositoryID, modulePath, version)
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	currentObjectKeys := make([]string, 0, 3)
	seen := make(map[string]bool)
	count, collected := 0, false
	for rows.Next() {
		var objectKey string
		var isCollected bool
		if err = rows.Scan(&objectKey, &isCollected); err != nil {
			_ = rows.Close()
			return GoModuleVersion{}, false, err
		}
		count++
		collected = collected || isCollected
		if objectKey != "" && !seen[objectKey] {
			seen[objectKey] = true
			currentObjectKeys = append(currentObjectKeys, objectKey)
		}
	}
	if err = rows.Close(); err != nil {
		return GoModuleVersion{}, false, err
	}
	if count != 3 || collected {
		return GoModuleVersion{}, false, ErrDisabled
	}
	sort.Strings(currentObjectKeys)
	if !slices.Equal(expectedObjectKeys, currentObjectKeys) {
		return GoModuleVersion{}, true, nil
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM artifact_tombstones
		WHERE repository_id::text=$1 AND format='go' AND coordinate=$2`, repositoryID, goModuleCoordinate(modulePath, version)); err != nil {
		return GoModuleVersion{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return GoModuleVersion{}, false, err
	}
	return item, false, nil
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
		  AND a.collecting_at IS NULL
		  AND a.collected_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t WHERE t.repository_id=a.repository_id AND t.format='go' AND t.coordinate=a.module_path || '@' || a.version)`, repositoryID, modulePath, version, kind), &asset)
	if errors.Is(err, sql.ErrNoRows) {
		return GoModuleAsset{}, ErrNotFound
	}
	return asset, err
}

func (s *PostgresStore) GoModuleObjectHasReference(ctx context.Context, objectKey string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_go_assets WHERE object_key=$1 AND collected_at IS NULL)`, objectKey).Scan(&referenced)
	return referenced, err
}

func (s *PostgresStore) ListReclaimableGoModuleObjects(ctx context.Context, before time.Time, limit int, after string) ([]GoModuleObject, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
		SELECT min(a.repository_id::text) AS repository_id,a.object_key,min(a.digest) AS digest,max(a.size) AS size,max(t.tombstoned_at) AS tombstoned_at
		FROM native_go_assets a
		JOIN artifact_tombstones t ON t.repository_id=a.repository_id AND t.format='go' AND t.coordinate=a.module_path || '@' || a.version
		WHERE a.collected_at IS NULL AND a.object_key>$2
		GROUP BY a.object_key HAVING max(t.tombstoned_at)<$1
	)
	SELECT c.repository_id,c.object_key,c.digest,c.size,c.tombstoned_at FROM candidates c
	WHERE NOT EXISTS (
		SELECT 1 FROM lifecycle_jobs j WHERE j.repository_id::text=c.repository_id AND j.kind='reclaim'
		  AND j.payload->>'format'='go' AND j.payload->>'tombstone'='true'
		  AND j.payload->>'objectKey'=c.object_key
		  AND (j.payload->>'tombstonedAt')::timestamptz=c.tombstoned_at
	)
	ORDER BY c.object_key LIMIT $3`, before, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	objects := make([]GoModuleObject, 0)
	for rows.Next() {
		var object GoModuleObject
		if err = rows.Scan(&object.RepositoryID, &object.ObjectKey, &object.Digest, &object.Size, &object.TombstonedAt); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

func (s *PostgresStore) GoModuleObjectHasVisibleReference(ctx context.Context, objectKey string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM native_go_assets a WHERE a.object_key=$1 AND a.collected_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM artifact_tombstones t
			WHERE t.repository_id=a.repository_id AND t.format='go' AND t.coordinate=a.module_path || '@' || a.version)
	)`, objectKey).Scan(&referenced)
	return referenced, err
}

func (s *PostgresStore) GoModuleObjectMatchesTombstone(ctx context.Context, objectKey string, expected time.Time) (bool, error) {
	var newest sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT max(t.tombstoned_at)
		FROM native_go_assets a
		JOIN artifact_tombstones t ON t.repository_id=a.repository_id AND t.format='go' AND t.coordinate=a.module_path || '@' || a.version
		WHERE a.object_key=$1 AND a.collected_at IS NULL`, objectKey).Scan(&newest)
	return newest.Valid && newest.Time.Equal(expected), err
}

func (s *PostgresStore) MarkGoModuleObjectCollecting(ctx context.Context, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_go_assets a SET collecting_at=COALESCE(a.collecting_at,now())
		WHERE a.object_key=$1 AND a.collected_at IS NULL
		  AND EXISTS (SELECT 1 FROM artifact_tombstones t
			WHERE t.repository_id=a.repository_id AND t.format='go' AND t.coordinate=a.module_path || '@' || a.version)`, objectKey)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) MarkGoModuleObjectCollected(ctx context.Context, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_go_assets a SET collecting_at=NULL,collected_at=now()
		WHERE a.object_key=$1 AND a.collected_at IS NULL
		  AND EXISTS (SELECT 1 FROM artifact_tombstones t
			WHERE t.repository_id=a.repository_id AND t.format='go' AND t.coordinate=a.module_path || '@' || a.version)`, objectKey)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

const goModuleVersionColumns = `repository_id::text,module_path,version,published_at,publisher,cached_at,created_at`
const goModuleAssetColumns = `repository_id::text,module_path,version,kind,digest,object_key,size,source_url,cached_at,created_at,collecting_at,collected_at`

func prefixedGoModuleVersionColumns(prefix string) string {
	return prefix + `.repository_id::text,` + prefix + `.module_path,` + prefix + `.version,` + prefix + `.published_at,` + prefix + `.publisher,` + prefix + `.cached_at,` + prefix + `.created_at`
}

func prefixedGoModuleAssetColumns(prefix string) string {
	return prefix + `.repository_id::text,` + prefix + `.module_path,` + prefix + `.version,` + prefix + `.kind,` + prefix + `.digest,` + prefix + `.object_key,` + prefix + `.size,` + prefix + `.source_url,` + prefix + `.cached_at,` + prefix + `.created_at,` + prefix + `.collecting_at,` + prefix + `.collected_at`
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
	var collectingAt, collectedAt sql.NullTime
	err := row.Scan(&asset.RepositoryID, &asset.Module, &asset.Version, &asset.Kind, &asset.Digest,
		&asset.ObjectKey, &asset.Size, &asset.SourceURL, &asset.CachedAt, &asset.CreatedAt, &collectingAt, &collectedAt)
	if collectingAt.Valid {
		asset.CollectingAt = collectingAt.Time
	}
	if collectedAt.Valid {
		asset.CollectedAt = collectedAt.Time
	}
	return err
}

func nullableGoTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
