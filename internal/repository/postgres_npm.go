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
		(repository_id,package_name,version,digest,integrity,shasum,tarball_name,object_key,size,manifest,publisher)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11)
		RETURNING created_at`, version.RepositoryID, version.PackageName, version.Version, version.Digest,
		version.Integrity, version.Shasum, version.TarballName, version.ObjectKey, version.Size, version.Manifest, version.Publisher).Scan(&version.CreatedAt)
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

func (s *PostgresStore) GetNPMPackage(ctx context.Context, repositoryID, name string) (NPMPackage, error) {
	var pkg NPMPackage
	var tags []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT repository_id::text,name,dist_tags,created_at,updated_at
		FROM native_npm_packages WHERE repository_id::text=$1 AND name=$2`, repositoryID, name).
		Scan(&pkg.RepositoryID, &pkg.Name, &tags, &pkg.CreatedAt, &pkg.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NPMPackage{}, ErrNotFound
	}
	if err != nil {
		return NPMPackage{}, err
	}
	if err = json.Unmarshal(tags, &pkg.DistTags); err != nil {
		return NPMPackage{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT repository_id::text,package_name,version,digest,integrity,shasum,
		       tarball_name,object_key,size,manifest,publisher,created_at
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
		       tarball_name,object_key,size,manifest,publisher,created_at
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
		WHERE p.repository_id::text=$1 AND ($2='' OR p.name LIKE $2 || '%' ESCAPE '\') AND p.name>$3
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
	return row.Scan(&version.RepositoryID, &version.PackageName, &version.Version, &version.Digest,
		&version.Integrity, &version.Shasum, &version.TarballName, &version.ObjectKey,
		&version.Size, &version.Manifest, &version.Publisher, &version.CreatedAt)
}
