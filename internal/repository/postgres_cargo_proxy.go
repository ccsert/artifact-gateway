package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func (s *PostgresStore) GetCargoProxyConfig(ctx context.Context, repositoryID string) (CargoProxyConfig, error) {
	var value CargoProxyConfig
	err := s.db.QueryRowContext(ctx, `SELECT repository_id::text,download_template,search_api,fetched_at,expires_at
		FROM native_cargo_proxy_configs WHERE repository_id::text=$1`, repositoryID).
		Scan(&value.RepositoryID, &value.DownloadTemplate, &value.SearchAPI, &value.FetchedAt, &value.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoProxyConfig{}, ErrNotFound
	}
	return value, err
}

func (s *PostgresStore) PutCargoProxyConfig(ctx context.Context, value CargoProxyConfig) error {
	if value.RepositoryID == "" || value.DownloadTemplate == "" || value.FetchedAt.IsZero() || !value.ExpiresAt.After(value.FetchedAt) {
		return ErrInvalidCargoIdentity
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO native_cargo_proxy_configs
		(repository_id,download_template,search_api,fetched_at,expires_at)
		SELECT id,$2,$3,$4,$5 FROM hosted_repositories
		WHERE id::text=$1 AND format='cargo' AND repo_type='proxy' AND state='active'
		ON CONFLICT (repository_id) DO UPDATE SET download_template=EXCLUDED.download_template,
		search_api=EXCLUDED.search_api,fetched_at=EXCLUDED.fetched_at,expires_at=EXCLUDED.expires_at`,
		value.RepositoryID, value.DownloadTemplate, value.SearchAPI, value.FetchedAt, value.ExpiresAt)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err == nil && count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) GetCargoProxyIndex(ctx context.Context, repositoryID, name string) (CargoProxyIndex, error) {
	identity, err := cargo.NormalizeIdentity(name, "0.0.0")
	if err != nil || repositoryID == "" {
		return CargoProxyIndex{}, ErrInvalidCargoIdentity
	}
	var value CargoProxyIndex
	err = scanCargoProxyIndex(s.db.QueryRowContext(ctx, `SELECT repository_id::text,name,body,status,upstream_etag,
		upstream_modified,fetched_at,expires_at FROM native_cargo_proxy_indexes
		WHERE repository_id::text=$1 AND collision_key=$2`, repositoryID, identity.CollisionKey), &value)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoProxyIndex{}, ErrNotFound
	}
	return value, err
}

func (s *PostgresStore) PutCargoProxyIndex(ctx context.Context, value CargoProxyIndex) error {
	identity, err := cargo.NormalizeIdentity(value.Name, "0.0.0")
	if err != nil || value.RepositoryID == "" {
		return ErrInvalidCargoIdentity
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockActiveCargoProxyRepository(ctx, tx, value.RepositoryID); err != nil {
		return err
	}
	var current CargoProxyIndex
	err = scanCargoProxyIndex(tx.QueryRowContext(ctx, `SELECT repository_id::text,name,body,status,upstream_etag,
		upstream_modified,fetched_at,expires_at FROM native_cargo_proxy_indexes
		WHERE repository_id::text=$1 AND collision_key=$2 FOR UPDATE`, value.RepositoryID, identity.CollisionKey), &current)
	var previous *CargoProxyIndex
	if err == nil {
		previous = &current
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous != nil && previous.Name != value.Name {
		return ErrUpstreamChanged
	}
	if err := validateCargoProxyIndex(value, previous); err != nil {
		return err
	}
	if value.Status == 200 {
		rows, err := tx.QueryContext(ctx, `SELECT version,checksum FROM native_cargo_proxy_crates
			WHERE repository_id::text=$1 AND collision_key=$2`, value.RepositoryID, identity.CollisionKey)
		if err != nil {
			return err
		}
		for rows.Next() {
			var version, checksum string
			if err := rows.Scan(&version, &checksum); err != nil {
				_ = rows.Close()
				return err
			}
			want, err := cargoProxyChecksum(value, version)
			if err != nil || want != checksum {
				_ = rows.Close()
				return ErrUpstreamChanged
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_cargo_proxy_indexes
		(repository_id,collision_key,name,body,status,upstream_etag,upstream_modified,fetched_at,expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (repository_id,collision_key) DO UPDATE SET body=EXCLUDED.body,status=EXCLUDED.status,
		upstream_etag=EXCLUDED.upstream_etag,upstream_modified=EXCLUDED.upstream_modified,
		fetched_at=EXCLUDED.fetched_at,expires_at=EXCLUDED.expires_at`,
		value.RepositoryID, identity.CollisionKey, value.Name, value.Body, value.Status,
		value.ETag, value.Modified, value.FetchedAt, value.ExpiresAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) GetCargoProxyCrate(ctx context.Context, repositoryID, name, version string) (CargoProxyCrate, error) {
	identity, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return CargoProxyCrate{}, ErrInvalidCargoIdentity
	}
	var value CargoProxyCrate
	err = s.db.QueryRowContext(ctx, `SELECT repository_id::text,name,version,checksum,object_key,size,cached_at
		FROM native_cargo_proxy_crates WHERE repository_id::text=$1 AND collision_key=$2 AND version_key=$3`,
		repositoryID, identity.CollisionKey, identity.VersionKey).
		Scan(&value.RepositoryID, &value.Name, &value.Version, &value.Checksum, &value.ObjectKey, &value.Size, &value.CachedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CargoProxyCrate{}, ErrNotFound
	}
	return value, err
}

func (s *PostgresStore) PutCargoProxyCrate(ctx context.Context, value CargoProxyCrate) error {
	identity, err := cargo.NormalizeIdentity(value.Name, value.Version)
	if err != nil || value.RepositoryID == "" {
		return ErrInvalidCargoIdentity
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockActiveCargoProxyRepository(ctx, tx, value.RepositoryID); err != nil {
		return err
	}
	var index CargoProxyIndex
	err = scanCargoProxyIndex(tx.QueryRowContext(ctx, `SELECT repository_id::text,name,body,status,upstream_etag,
		upstream_modified,fetched_at,expires_at FROM native_cargo_proxy_indexes
		WHERE repository_id::text=$1 AND collision_key=$2 FOR UPDATE`, value.RepositoryID, identity.CollisionKey), &index)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := validateCargoProxyCrate(value, index); err != nil {
		return err
	}
	var checksum string
	var size int64
	err = tx.QueryRowContext(ctx, `SELECT checksum,size FROM native_cargo_proxy_crates
		WHERE repository_id::text=$1 AND collision_key=$2 AND version_key=$3 FOR UPDATE`,
		value.RepositoryID, identity.CollisionKey, identity.VersionKey).Scan(&checksum, &size)
	if err == nil && (checksum != value.Checksum || size != value.Size) {
		return ErrUpstreamChanged
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO native_cargo_proxy_crates
		(repository_id,collision_key,version_key,name,version,checksum,object_key,size,cached_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (repository_id,collision_key,version_key) DO NOTHING`,
		value.RepositoryID, identity.CollisionKey, identity.VersionKey, value.Name, value.Version,
		value.Checksum, value.ObjectKey, value.Size, value.CachedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func scanCargoProxyIndex(row interface{ Scan(...any) error }, value *CargoProxyIndex) error {
	return row.Scan(&value.RepositoryID, &value.Name, &value.Body, &value.Status, &value.ETag,
		&value.Modified, &value.FetchedAt, &value.ExpiresAt)
}

func lockActiveCargoProxyRepository(ctx context.Context, tx *sql.Tx, repositoryID string) error {
	var format Format
	var repoType RepositoryType
	var state RepositoryState
	err := tx.QueryRowContext(ctx, `SELECT format,repo_type,state FROM hosted_repositories
		WHERE id::text=$1 FOR UPDATE`, repositoryID).Scan(&format, &repoType, &state)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (format != FormatCargo || repoType != RepositoryTypeProxy || state != RepositoryActive) {
		return ErrNotFound
	}
	return err
}
