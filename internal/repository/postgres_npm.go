package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	lockKey := "native-npm-proxy:" + key
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockKey)
		_ = conn.Close()
	}, nil
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
		       tarball_name,upstream_tarball,object_key,size,manifest,publisher,cached_at,created_at
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
		          tarball_name,upstream_tarball,object_key,size,manifest,publisher,cached_at,created_at`,
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
		       tarball_name,upstream_tarball,object_key,size,manifest,publisher,cached_at,created_at
		FROM native_npm_versions
		WHERE repository_id::text=$1 AND package_name=$2
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
	return pkg, rows.Err()
}

func (s *PostgresStore) GetNPMVersionByTarball(ctx context.Context, repositoryID, name, tarballName string) (NPMVersion, error) {
	var version NPMVersion
	err := scanNPMVersion(s.db.QueryRowContext(ctx, `
		SELECT repository_id::text,package_name,version,digest,integrity,shasum,
		       tarball_name,upstream_tarball,object_key,size,manifest,publisher,cached_at,created_at
		FROM native_npm_versions
		WHERE repository_id::text=$1 AND package_name=$2 AND tarball_name=$3`, repositoryID, name, tarballName), &version)
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
		JOIN native_npm_versions v ON v.repository_id=p.repository_id AND v.package_name=p.name
		LEFT JOIN LATERAL (
			SELECT nv.* FROM native_npm_versions nv
			WHERE nv.repository_id=p.repository_id AND nv.package_name=p.name
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
	var cachedAt sql.NullTime
	err := row.Scan(&version.RepositoryID, &version.PackageName, &version.Version, &version.Digest,
		&version.Integrity, &version.Shasum, &version.TarballName, &version.UpstreamTarball, &version.ObjectKey,
		&version.Size, &version.Manifest, &version.Publisher, &cachedAt, &version.CreatedAt)
	if cachedAt.Valid {
		version.CachedAt = cachedAt.Time
	}
	return err
}
