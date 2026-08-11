package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *PostgresStore) PublishNPMVersion(ctx context.Context, version NPMVersion, distTags map[string]string) (NPMVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NPMVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx, `
		INSERT INTO native_npm_packages (repository_id,name,dist_tags)
		VALUES ($1,$2,'{}'::jsonb)
		ON CONFLICT (repository_id,name) DO NOTHING`, version.RepositoryID, version.PackageName); err != nil {
		return NPMVersion{}, err
	}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO native_npm_versions
		(repository_id,package_name,version,digest,integrity,shasum,tarball_name,object_key,size,manifest,publisher,cached_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,now())
		RETURNING created_at,cached_at`, version.RepositoryID, version.PackageName, version.Version, version.Digest,
		version.Integrity, version.Shasum, version.TarballName, version.ObjectKey, version.Size, version.Manifest, version.Publisher).Scan(&version.CreatedAt, &version.CachedAt)
	if isUnique(err) {
		return NPMVersion{}, ErrNameExists
	}
	if err != nil {
		return NPMVersion{}, err
	}
	for _, target := range distTags {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM native_npm_versions
			WHERE repository_id::text=$1 AND package_name=$2 AND version=$3
		)`, version.RepositoryID, version.PackageName, target).Scan(&exists); err != nil {
			return NPMVersion{}, err
		}
		if !exists {
			return NPMVersion{}, ErrNotFound
		}
	}
	tagsJSON, err := json.Marshal(distTags)
	if err != nil {
		return NPMVersion{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE native_npm_packages
		SET dist_tags=dist_tags || $3::jsonb,updated_at=now()
		WHERE repository_id::text=$1 AND name=$2`, version.RepositoryID, version.PackageName, tagsJSON); err != nil {
		return NPMVersion{}, err
	}
	if err = tx.Commit(); err != nil {
		return NPMVersion{}, err
	}
	return version, nil
}

func (s *PostgresStore) LockNPMProxy(ctx context.Context, key string) (func(), error) {
	_, release, err := s.LockNPMProxyWithContext(ctx, key)
	return release, err
}

func (s *PostgresStore) LockNPMProxyWithContext(ctx context.Context, key string) (context.Context, func(), error) {
	return s.lockPostgresAdvisoryKeys(ctx, []string{"native-npm-proxy:" + key})
}

func (s *PostgresStore) LockNPMObject(ctx context.Context, objectKey string) (func(), error) {
	_, release, err := s.LockArtifactObjectKeys(ctx, FormatNPM, []string{objectKey})
	return release, err
}

func (s *PostgresStore) SyncNPMProxyPackage(ctx context.Context, incoming NPMPackage) (NPMPackage, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NPMPackage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	lockKey := "native-npm-proxy-package:" + incoming.RepositoryID + ":" + incoming.Name
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return NPMPackage{}, err
	}
	var existingEndpoint string
	err = tx.QueryRowContext(ctx, `
		SELECT source_endpoint FROM native_npm_packages
		WHERE repository_id::text=$1 AND name=$2 FOR UPDATE`, incoming.RepositoryID, incoming.Name).Scan(&existingEndpoint)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO native_npm_packages (repository_id,name,dist_tags,source_endpoint)
			VALUES ($1,$2,'{}'::jsonb,$3)`, incoming.RepositoryID, incoming.Name, incoming.SourceEndpoint)
	} else if err == nil && existingEndpoint != "" && existingEndpoint != incoming.SourceEndpoint {
		_, err = tx.ExecContext(ctx, `DELETE FROM native_npm_packages WHERE repository_id::text=$1 AND name=$2`, incoming.RepositoryID, incoming.Name)
		if err == nil {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO native_npm_packages (repository_id,name,dist_tags,source_endpoint)
				VALUES ($1,$2,'{}'::jsonb,$3)`, incoming.RepositoryID, incoming.Name, incoming.SourceEndpoint)
		}
	}
	if err != nil {
		return NPMPackage{}, err
	}

	for _, version := range incoming.Versions {
		var storedIntegrity, storedShasum, storedTarball, storedObject string
		lookupErr := tx.QueryRowContext(ctx, `
			SELECT integrity,shasum,upstream_tarball,object_key
			FROM native_npm_versions
			WHERE repository_id::text=$1 AND package_name=$2 AND version=$3 FOR UPDATE`,
			incoming.RepositoryID, incoming.Name, version.Version).
			Scan(&storedIntegrity, &storedShasum, &storedTarball, &storedObject)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, `
				INSERT INTO native_npm_versions
				(repository_id,package_name,version,digest,integrity,shasum,tarball_name,upstream_tarball,
				 object_key,size,manifest,publisher,created_at)
				VALUES ($1,$2,$3,'',$4,$5,$6,$7,'',0,$8::jsonb,$9,$10)`,
				incoming.RepositoryID, incoming.Name, version.Version, version.Integrity, version.Shasum,
				version.TarballName, version.UpstreamTarball, version.Manifest, version.Publisher, version.CreatedAt)
		} else if lookupErr != nil {
			return NPMPackage{}, lookupErr
		} else {
			if storedObject != "" && (storedIntegrity != version.Integrity || storedShasum != version.Shasum || storedTarball != version.UpstreamTarball) {
				return NPMPackage{}, ErrUpstreamChanged
			}
			_, err = tx.ExecContext(ctx, `
				UPDATE native_npm_versions
				SET integrity=$4,shasum=$5,tarball_name=$6,upstream_tarball=$7,
				    manifest=$8::jsonb,publisher=$9
				WHERE repository_id::text=$1 AND package_name=$2 AND version=$3`,
				incoming.RepositoryID, incoming.Name, version.Version, version.Integrity, version.Shasum,
				version.TarballName, version.UpstreamTarball, version.Manifest, version.Publisher)
		}
		if err != nil {
			return NPMPackage{}, err
		}
	}
	for _, target := range incoming.DistTags {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM native_npm_versions
			WHERE repository_id::text=$1 AND package_name=$2 AND version=$3
		)`, incoming.RepositoryID, incoming.Name, target).Scan(&exists); err != nil {
			return NPMPackage{}, err
		}
		if !exists {
			return NPMPackage{}, ErrNotFound
		}
	}
	tagsJSON, err := json.Marshal(incoming.DistTags)
	if err != nil {
		return NPMPackage{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE native_npm_packages
		SET dist_tags=$3::jsonb,source_endpoint=$4,upstream_etag=$5,upstream_modified=$6,
		    metadata_expires_at=$7,negative_expires_at=NULL,negative=false,updated_at=now()
		WHERE repository_id::text=$1 AND name=$2`, incoming.RepositoryID, incoming.Name, tagsJSON,
		incoming.SourceEndpoint, incoming.UpstreamETag, incoming.UpstreamModified, incoming.MetadataExpiresAt); err != nil {
		return NPMPackage{}, err
	}
	if err = tx.Commit(); err != nil {
		return NPMPackage{}, err
	}
	return s.GetNPMPackage(ctx, incoming.RepositoryID, incoming.Name)
}

func (s *PostgresStore) StoreNPMProxyNegative(ctx context.Context, incoming NPMPackage) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	lockKey := "native-npm-proxy-package:" + incoming.RepositoryID + ":" + incoming.Name
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return err
	}
	var existingEndpoint string
	err = tx.QueryRowContext(ctx, `SELECT source_endpoint FROM native_npm_packages WHERE repository_id::text=$1 AND name=$2 FOR UPDATE`, incoming.RepositoryID, incoming.Name).Scan(&existingEndpoint)
	if err == nil && existingEndpoint != "" && existingEndpoint != incoming.SourceEndpoint {
		if _, err = tx.ExecContext(ctx, `DELETE FROM native_npm_packages WHERE repository_id::text=$1 AND name=$2`, incoming.RepositoryID, incoming.Name); err != nil {
			return err
		}
		err = sql.ErrNoRows
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO native_npm_packages
			(repository_id,name,dist_tags,source_endpoint,negative_expires_at,negative)
			VALUES ($1,$2,'{}'::jsonb,$3,$4,true)`, incoming.RepositoryID, incoming.Name, incoming.SourceEndpoint, incoming.NegativeExpiresAt)
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `
			UPDATE native_npm_packages
			SET source_endpoint=$3,metadata_expires_at=NULL,negative_expires_at=$4,negative=true,updated_at=now()
			WHERE repository_id::text=$1 AND name=$2`, incoming.RepositoryID, incoming.Name, incoming.SourceEndpoint, incoming.NegativeExpiresAt)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) CacheNPMProxyTarball(ctx context.Context, incoming NPMVersion) (NPMVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NPMVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var stored NPMVersion
	err = scanNPMVersion(tx.QueryRowContext(ctx, `
		SELECT repository_id::text,package_name,version,digest,integrity,shasum,
		       tarball_name,upstream_tarball,object_key,size,manifest,publisher,state,cached_at,deleted_at,collected_at,created_at
		FROM native_npm_versions
		WHERE repository_id::text=$1 AND package_name=$2 AND version=$3 FOR UPDATE`,
		incoming.RepositoryID, incoming.PackageName, incoming.Version), &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return NPMVersion{}, ErrNotFound
	}
	if err != nil {
		return NPMVersion{}, err
	}
	if stored.ObjectKey != "" {
		if stored.Digest != incoming.Digest {
			return NPMVersion{}, ErrUpstreamChanged
		}
		return stored, tx.Commit()
	}
	err = scanNPMVersion(tx.QueryRowContext(ctx, `
		UPDATE native_npm_versions
		SET digest=$4,object_key=$5,size=$6,cached_at=$7
		WHERE repository_id::text=$1 AND package_name=$2 AND version=$3
		RETURNING repository_id::text,package_name,version,digest,integrity,shasum,
		          tarball_name,upstream_tarball,object_key,size,manifest,publisher,state,cached_at,deleted_at,collected_at,created_at`,
		incoming.RepositoryID, incoming.PackageName, incoming.Version, incoming.Digest,
		incoming.ObjectKey, incoming.Size, incoming.CachedAt), &stored)
	if err != nil {
		return NPMVersion{}, err
	}
	if err = tx.Commit(); err != nil {
		return NPMVersion{}, err
	}
	return stored, nil
}

func (s *PostgresStore) GetNPMPackage(ctx context.Context, repositoryID, name string) (NPMPackage, error) {
	var pkg NPMPackage
	var tags []byte
	var metadataExpiresAt, negativeExpiresAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT repository_id::text,name,dist_tags,source_endpoint,upstream_etag,upstream_modified,
		       metadata_expires_at,negative_expires_at,negative,created_at,updated_at
		FROM native_npm_packages WHERE repository_id::text=$1 AND name=$2`, repositoryID, name).
		Scan(&pkg.RepositoryID, &pkg.Name, &tags, &pkg.SourceEndpoint, &pkg.UpstreamETag, &pkg.UpstreamModified,
			&metadataExpiresAt, &negativeExpiresAt, &pkg.Negative, &pkg.CreatedAt, &pkg.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NPMPackage{}, ErrNotFound
	}
	if err != nil {
		return NPMPackage{}, err
	}
	if err = json.Unmarshal(tags, &pkg.DistTags); err != nil {
		return NPMPackage{}, err
	}
	if metadataExpiresAt.Valid {
		pkg.MetadataExpiresAt = metadataExpiresAt.Time
	}
	if negativeExpiresAt.Valid {
		pkg.NegativeExpiresAt = negativeExpiresAt.Time
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT repository_id::text,package_name,version,digest,integrity,shasum,
		       tarball_name,upstream_tarball,object_key,size,manifest,publisher,state,cached_at,deleted_at,collected_at,created_at
		FROM native_npm_versions
		WHERE repository_id::text=$1 AND package_name=$2 AND state='visible'
		ORDER BY created_at DESC,version DESC`, repositoryID, name)
	if err != nil {
		return NPMPackage{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var version NPMVersion
		if err = scanNPMVersion(rows, &version); err != nil {
			return NPMPackage{}, err
		}
		pkg.Versions = append(pkg.Versions, version)
	}
	if err = rows.Err(); err != nil {
		return NPMPackage{}, err
	}
	if len(pkg.Versions) == 0 && !pkg.Negative {
		return NPMPackage{}, ErrNotFound
	}
	return pkg, nil
}

func (s *PostgresStore) ListNPMVersions(ctx context.Context, repositoryID, name string) ([]NPMVersion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT repository_id::text,package_name,version,digest,integrity,shasum,
		       tarball_name,upstream_tarball,object_key,size,manifest,publisher,state,cached_at,deleted_at,collected_at,created_at
		FROM native_npm_versions
		WHERE repository_id::text=$1 AND package_name=$2 AND state='visible'
		ORDER BY created_at DESC,version DESC`, repositoryID, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	versions := make([]NPMVersion, 0)
	for rows.Next() {
		var version NPMVersion
		if err = scanNPMVersion(rows, &version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func (s *PostgresStore) GetNPMVersion(ctx context.Context, repositoryID, name, version string) (NPMVersion, error) {
	var item NPMVersion
	err := scanNPMVersion(s.db.QueryRowContext(ctx, `
		SELECT repository_id::text,package_name,version,digest,integrity,shasum,
		       tarball_name,upstream_tarball,object_key,size,manifest,publisher,state,cached_at,deleted_at,collected_at,created_at
		FROM native_npm_versions
		WHERE repository_id::text=$1 AND package_name=$2 AND version=$3 AND state='visible'`, repositoryID, name, version), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return NPMVersion{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) TombstoneNPMVersion(ctx context.Context, repositoryID, name, version string) (NPMVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NPMVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var item NPMVersion
	err = scanNPMVersion(tx.QueryRowContext(ctx, `
		UPDATE native_npm_versions SET state='deleted',deleted_at=now()
		WHERE repository_id::text=$1 AND package_name=$2 AND version=$3 AND state='visible'
		RETURNING repository_id::text,package_name,version,digest,integrity,shasum,
		          tarball_name,upstream_tarball,object_key,size,manifest,publisher,state,cached_at,deleted_at,collected_at,created_at`,
		repositoryID, name, version), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return NPMVersion{}, ErrNotFound
	}
	if err != nil {
		return NPMVersion{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE native_npm_packages
		SET dist_tags=COALESCE((SELECT jsonb_object_agg(key,value) FROM jsonb_each_text(dist_tags) WHERE value<>$3),'{}'::jsonb),updated_at=now()
		WHERE repository_id::text=$1 AND name=$2`, repositoryID, name, version); err != nil {
		return NPMVersion{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO artifact_tombstones (repository_id,format,coordinate,digest)
		VALUES ($1,'npm',$2,$3)
		ON CONFLICT (repository_id,format,coordinate)
		DO UPDATE SET digest=EXCLUDED.digest,tombstoned_at=now()`, repositoryID, name+"@"+version, item.Digest); err != nil {
		return NPMVersion{}, err
	}
	if err = tx.Commit(); err != nil {
		return NPMVersion{}, err
	}
	return item, nil
}

func (s *PostgresStore) RestoreNPMVersion(ctx context.Context, repositoryID, name, version string) (NPMVersion, error) {
	var objectKey string
	err := s.db.QueryRowContext(ctx, `
		SELECT object_key FROM native_npm_versions
		WHERE repository_id::text=$1 AND package_name=$2 AND version=$3 AND state='deleted'`,
		repositoryID, name, version).Scan(&objectKey)
	if errors.Is(err, sql.ErrNoRows) {
		return NPMVersion{}, ErrDisabled
	}
	if err != nil {
		return NPMVersion{}, err
	}
	release, err := s.LockNPMObject(ctx, objectKey)
	if err != nil {
		return NPMVersion{}, err
	}
	defer release()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NPMVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var item NPMVersion
	err = scanNPMVersion(tx.QueryRowContext(ctx, `
		UPDATE native_npm_versions SET state='visible',deleted_at=NULL
		WHERE repository_id::text=$1 AND package_name=$2 AND version=$3 AND state='deleted' AND collected_at IS NULL
		RETURNING repository_id::text,package_name,version,digest,integrity,shasum,
		          tarball_name,upstream_tarball,object_key,size,manifest,publisher,state,cached_at,deleted_at,collected_at,created_at`,
		repositoryID, name, version), &item)
	if errors.Is(err, sql.ErrNoRows) {
		return NPMVersion{}, ErrDisabled
	}
	if err != nil {
		return NPMVersion{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE native_npm_packages
		SET dist_tags=CASE WHEN dist_tags ? 'latest' THEN dist_tags ELSE dist_tags || jsonb_build_object('latest',$3::text) END,updated_at=now()
		WHERE repository_id::text=$1 AND name=$2`, repositoryID, name, version); err != nil {
		return NPMVersion{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM artifact_tombstones WHERE repository_id::text=$1 AND format='npm' AND coordinate=$2`, repositoryID, name+"@"+version); err != nil {
		return NPMVersion{}, err
	}
	if err = tx.Commit(); err != nil {
		return NPMVersion{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListReclaimableNPMObjects(ctx context.Context, before time.Time, limit int) ([]NPMObject, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT min(d.repository_id::text),d.object_key,min(d.digest),max(d.size),min(d.deleted_at)
		FROM native_npm_versions d
		WHERE d.state='deleted' AND d.collected_at IS NULL AND d.object_key<>'' AND d.deleted_at<$1
		  AND NOT EXISTS (SELECT 1 FROM native_npm_versions v WHERE v.object_key=d.object_key AND v.state='visible')
		GROUP BY d.object_key ORDER BY d.object_key LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	objects := make([]NPMObject, 0)
	for rows.Next() {
		var object NPMObject
		if err = rows.Scan(&object.RepositoryID, &object.ObjectKey, &object.Digest, &object.Size, &object.DeletedAt); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, rows.Err()
}

func (s *PostgresStore) NPMObjectHasVisibleReference(ctx context.Context, objectKey string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_npm_versions WHERE object_key=$1 AND state='visible')`, objectKey).Scan(&referenced)
	return referenced, err
}

func (s *PostgresStore) MarkNPMObjectCollected(ctx context.Context, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE native_npm_versions SET collected_at=now()
		WHERE object_key=$1 AND state='deleted' AND collected_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM native_npm_versions v WHERE v.object_key=$1 AND v.state='visible')`, objectKey)
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

func (s *PostgresStore) GetNPMVersionByTarball(ctx context.Context, repositoryID, name, tarballName string) (NPMVersion, error) {
	var version NPMVersion
	err := scanNPMVersion(s.db.QueryRowContext(ctx, `
		SELECT repository_id::text,package_name,version,digest,integrity,shasum,
		       tarball_name,upstream_tarball,object_key,size,manifest,publisher,state,cached_at,deleted_at,collected_at,created_at
		FROM native_npm_versions
		WHERE repository_id::text=$1 AND package_name=$2 AND tarball_name=$3 AND state='visible'`, repositoryID, name, tarballName), &version)
	if errors.Is(err, sql.ErrNoRows) {
		return NPMVersion{}, ErrNotFound
	}
	return version, err
}

func (s *PostgresStore) SearchNPMPackages(ctx context.Context, repositoryID, prefix string, limit int, after string) ([]NPMPackageSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.repository_id::text,p.name,p.created_at,p.updated_at,COUNT(v.version)::int,
		       COALESCE(latest.version,''),COALESCE(latest.digest,''),COALESCE(latest.integrity,''),
		       COALESCE(latest.shasum,''),COALESCE(latest.tarball_name,''),COALESCE(latest.object_key,''),
		       COALESCE(latest.size,0),COALESCE(latest.manifest,'{}'::jsonb),COALESCE(latest.publisher,''),latest.created_at
		FROM native_npm_packages p
		JOIN native_npm_versions v ON v.repository_id=p.repository_id AND v.package_name=p.name AND v.state='visible'
		LEFT JOIN LATERAL (
			SELECT nv.* FROM native_npm_versions nv
			WHERE nv.repository_id=p.repository_id AND nv.package_name=p.name AND nv.state='visible'
			ORDER BY (nv.version=p.dist_tags->>'latest') DESC,nv.created_at DESC,nv.version DESC
			LIMIT 1
		) latest ON true
		WHERE p.repository_id::text=$1 AND NOT p.negative AND ($2='' OR p.name LIKE $2 || '%' ESCAPE '\') AND p.name>$3
		GROUP BY p.repository_id,p.name,p.created_at,p.updated_at,latest.version,latest.digest,latest.integrity,
		         latest.shasum,latest.tarball_name,latest.object_key,latest.size,latest.manifest,latest.publisher,latest.created_at
		ORDER BY p.name LIMIT $4`, repositoryID, escapeLikePrefix(prefix), after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	summaries := make([]NPMPackageSummary, 0, limit)
	for rows.Next() {
		var summary NPMPackageSummary
		var latestCreated sql.NullTime
		if err = rows.Scan(&summary.RepositoryID, &summary.Name, &summary.CreatedAt, &summary.UpdatedAt, &summary.VersionCount,
			&summary.Latest.Version, &summary.Latest.Digest, &summary.Latest.Integrity, &summary.Latest.Shasum,
			&summary.Latest.TarballName, &summary.Latest.ObjectKey, &summary.Latest.Size, &summary.Latest.Manifest,
			&summary.Latest.Publisher, &latestCreated); err != nil {
			return nil, err
		}
		summary.Latest.RepositoryID = summary.RepositoryID
		summary.Latest.PackageName = summary.Name
		if latestCreated.Valid {
			summary.Latest.CreatedAt = latestCreated.Time
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}

func scanNPMVersion(row interface{ Scan(...any) error }, version *NPMVersion) error {
	var cachedAt, deletedAt, collectedAt sql.NullTime
	err := row.Scan(&version.RepositoryID, &version.PackageName, &version.Version, &version.Digest,
		&version.Integrity, &version.Shasum, &version.TarballName, &version.UpstreamTarball, &version.ObjectKey,
		&version.Size, &version.Manifest, &version.Publisher, &version.State, &cachedAt, &deletedAt, &collectedAt, &version.CreatedAt)
	if cachedAt.Valid {
		version.CachedAt = cachedAt.Time
	}
	if deletedAt.Valid {
		version.DeletedAt = deletedAt.Time
	}
	if collectedAt.Valid {
		version.CollectedAt = collectedAt.Time
	}
	return err
}
