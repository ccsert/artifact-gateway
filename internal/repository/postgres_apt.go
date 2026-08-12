package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) LockAPTObject(ctx context.Context, objectKey string) (func(), error) {
	_, release, err := s.lockPostgresAdvisoryKeys(ctx, []string{"native-apt-object:" + objectKey})
	return release, err
}

func (s *PostgresStore) GetAPTAsset(ctx context.Context, repositoryID, path string) (APTAsset, error) {
	var asset APTAsset
	err := scanAPTAsset(s.db.QueryRowContext(ctx, `SELECT `+aptAssetColumns+` FROM native_apt_assets WHERE repository_id::text=$1 AND path=$2`, repositoryID, path), &asset)
	if errors.Is(err, sql.ErrNoRows) {
		return APTAsset{}, ErrNotFound
	}
	return asset, err
}

func (s *PostgresStore) CacheAPTAsset(ctx context.Context, incoming APTAsset) (APTAsset, error) {
	var stored APTAsset
	err := scanAPTAsset(s.db.QueryRowContext(ctx, `INSERT INTO native_apt_assets
		(repository_id,path,digest,object_key,size,content_type,source_url,upstream_etag,upstream_modified,cached_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,now())
		ON CONFLICT (repository_id,path) DO UPDATE SET
			digest=EXCLUDED.digest,object_key=EXCLUDED.object_key,size=EXCLUDED.size,
			content_type=EXCLUDED.content_type,source_url=EXCLUDED.source_url,
			upstream_etag=EXCLUDED.upstream_etag,upstream_modified=EXCLUDED.upstream_modified,cached_at=now()
		WHERE (native_apt_assets.digest=EXCLUDED.digest AND native_apt_assets.object_key=EXCLUDED.object_key
		       AND native_apt_assets.size=EXCLUDED.size AND native_apt_assets.source_url=EXCLUDED.source_url)
		   OR (native_apt_assets.path LIKE 'dists/%' AND position('/by-hash/' in native_apt_assets.path)=0)
		RETURNING `+aptAssetColumns,
		incoming.RepositoryID, incoming.Path, incoming.Digest, incoming.ObjectKey, incoming.Size, incoming.ContentType, incoming.SourceURL, incoming.UpstreamETag, incoming.UpstreamModified), &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return APTAsset{}, ErrUpstreamChanged
	}
	if IsQuotaExceeded(err) {
		return APTAsset{}, ErrQuotaExceeded
	}
	if err != nil {
		return APTAsset{}, err
	}
	if stored.Digest != incoming.Digest || stored.ObjectKey != incoming.ObjectKey || stored.Size != incoming.Size || stored.SourceURL != incoming.SourceURL {
		return APTAsset{}, ErrUpstreamChanged
	}
	return stored, nil
}

func (s *PostgresStore) ListAPTAssets(ctx context.Context, repositoryID, prefix string, limit int, after string) ([]APTAsset, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+aptAssetColumns+` FROM native_apt_assets WHERE repository_id::text=$1 AND path LIKE $2 || '%' ESCAPE '\' AND path>$3 ORDER BY path LIMIT $4`, repositoryID, escapeLikePrefix(prefix), after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]APTAsset, 0)
	for rows.Next() {
		var asset APTAsset
		if err = scanAPTAsset(rows, &asset); err != nil {
			return nil, err
		}
		items = append(items, asset)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const aptAssetColumns = `repository_id::text,path,digest,object_key,size,content_type,source_url,upstream_etag,upstream_modified,cached_at,created_at`

func scanAPTAsset(row interface{ Scan(...any) error }, asset *APTAsset) error {
	return row.Scan(&asset.RepositoryID, &asset.Path, &asset.Digest, &asset.ObjectKey, &asset.Size, &asset.ContentType, &asset.SourceURL, &asset.UpstreamETag, &asset.UpstreamModified, &asset.CachedAt, &asset.CreatedAt)
}
