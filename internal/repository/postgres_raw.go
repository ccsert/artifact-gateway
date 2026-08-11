package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *PostgresStore) CreateRawUpload(ctx context.Context, v RawUpload) (RawUpload, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO native_raw_uploads (id,repository_id,path,object_key,byte_offset,state,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, v.ID, v.RepositoryID, v.Path, v.ObjectKey, v.Offset, v.State, v.ExpiresAt)
	return v, err
}
func (s *PostgresStore) LockRawUpload(ctx context.Context, id string) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, "native-raw-upload:"+id); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "native-raw-upload:"+id)
		_ = conn.Close()
	}, nil
}
func (s *PostgresStore) GetRawUpload(ctx context.Context, id string) (RawUpload, error) {
	var v RawUpload
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,path,object_key,byte_offset,state,expires_at FROM native_raw_uploads WHERE id::text=$1`, id).Scan(&v.ID, &v.RepositoryID, &v.Path, &v.ObjectKey, &v.Offset, &v.State, &v.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RawUpload{}, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) UpdateRawUpload(ctx context.Context, id string, offset int64) (RawUpload, error) {
	var v RawUpload
	err := s.db.QueryRowContext(ctx, `UPDATE native_raw_uploads SET byte_offset=$2 WHERE id::text=$1 AND state='open' AND expires_at>now() RETURNING id::text,repository_id::text,path,object_key,byte_offset,state,expires_at`, id, offset).Scan(&v.ID, &v.RepositoryID, &v.Path, &v.ObjectKey, &v.Offset, &v.State, &v.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RawUpload{}, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) CancelRawUpload(ctx context.Context, id string) (RawUpload, error) {
	var v RawUpload
	err := s.db.QueryRowContext(ctx, `UPDATE native_raw_uploads SET state='cancelled' WHERE id::text=$1 AND state='open' RETURNING id::text,repository_id::text,path,object_key,byte_offset,state,expires_at`, id).Scan(&v.ID, &v.RepositoryID, &v.Path, &v.ObjectKey, &v.Offset, &v.State, &v.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RawUpload{}, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) CompleteRawUpload(ctx context.Context, id string, asset RawAsset) (RawAsset, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return asset, err
	}
	defer func() { _ = tx.Rollback() }()
	var repoID, path string
	err = tx.QueryRowContext(ctx, `SELECT repository_id::text,path FROM native_raw_uploads WHERE id::text=$1 AND state='open' AND expires_at>now() FOR UPDATE`, id).Scan(&repoID, &path)
	if errors.Is(err, sql.ErrNoRows) {
		return asset, ErrNotFound
	}
	if err != nil {
		return asset, err
	}
	if repoID != asset.RepositoryID || path != asset.Path {
		return asset, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_raw_objects (digest,repository_id,object_key,size) VALUES ($1,$2,$3,$4) ON CONFLICT (digest) DO UPDATE SET repository_id=EXCLUDED.repository_id,collected_at=NULL`, asset.Digest, asset.RepositoryID, asset.ObjectKey, asset.Size); err != nil {
		return asset, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_raw_assets (repository_id,path,digest,content_type) VALUES ($1,$2,$3,$4) ON CONFLICT (repository_id,path) DO UPDATE SET digest=EXCLUDED.digest,content_type=EXCLUDED.content_type,updated_at=now()`, asset.RepositoryID, asset.Path, asset.Digest, asset.ContentType); err != nil {
		return asset, err
	}
	asset.UpdatedAt = time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE native_raw_uploads SET state='completed' WHERE id::text=$1`, id); err != nil {
		return asset, err
	}
	return asset, tx.Commit()
}

func (s *PostgresStore) PutRawAsset(ctx context.Context, v RawAsset) (RawAsset, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_raw_objects (digest,repository_id,object_key,size) VALUES ($1,$2,$3,$4) ON CONFLICT (digest) DO UPDATE SET repository_id=EXCLUDED.repository_id,collected_at=NULL`, v.Digest, v.RepositoryID, v.ObjectKey, v.Size); err != nil {
		return v, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT object_key,size FROM native_raw_objects WHERE digest=$1`, v.Digest).Scan(&v.ObjectKey, &v.Size); err != nil {
		return v, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_raw_assets (repository_id,path,digest,content_type) VALUES ($1,$2,$3,$4) ON CONFLICT (repository_id,path) DO UPDATE SET digest=EXCLUDED.digest,content_type=EXCLUDED.content_type,updated_at=now()`, v.RepositoryID, v.Path, v.Digest, v.ContentType); err != nil {
		return v, err
	}
	v.UpdatedAt = time.Now().UTC()
	return v, tx.Commit()
}
func (s *PostgresStore) StageRawObject(ctx context.Context, object RawObject) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO native_raw_objects (digest,repository_id,object_key,size) VALUES ($1,$2,$3,$4) ON CONFLICT (digest) DO UPDATE SET repository_id=COALESCE(native_raw_objects.repository_id,EXCLUDED.repository_id),collected_at=NULL`, object.Digest, nullableString(object.RepositoryID), object.ObjectKey, object.Size)
	return err
}
func (s *PostgresStore) LockRawObject(ctx context.Context, digest string) (func(), error) {
	_, release, err := s.LockArtifactObjectKeys(ctx, FormatRaw, []string{digest})
	return release, err
}
func (s *PostgresStore) GetRawAsset(ctx context.Context, repositoryID, path string) (RawAsset, error) {
	var v RawAsset
	err := s.db.QueryRowContext(ctx, `SELECT a.repository_id::text,a.path,a.digest,o.object_key,o.size,a.content_type,a.updated_at FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest WHERE a.repository_id::text=$1 AND a.path=$2`, repositoryID, path).Scan(&v.RepositoryID, &v.Path, &v.Digest, &v.ObjectKey, &v.Size, &v.ContentType, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) ListRawAssets(ctx context.Context, repositoryID, prefix string, limit int, after string) ([]RawAsset, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.repository_id::text,a.path,a.digest,o.object_key,o.size,a.content_type,a.updated_at FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest WHERE a.repository_id::text=$1 AND ($2='' OR a.path LIKE $2 || '%' ESCAPE '\') AND a.path>$3 ORDER BY a.path LIMIT $4`, repositoryID, escapeLikePrefix(prefix), after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	assets := make([]RawAsset, 0)
	for rows.Next() {
		var asset RawAsset
		if err := rows.Scan(&asset.RepositoryID, &asset.Path, &asset.Digest, &asset.ObjectKey, &asset.Size, &asset.ContentType, &asset.UpdatedAt); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}
func (s *PostgresStore) DeleteRawAsset(ctx context.Context, repositoryID, path string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var asset RawAsset
	err = tx.QueryRowContext(ctx, `SELECT a.repository_id::text,a.path,a.digest,o.object_key,o.size,a.content_type,a.updated_at FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest WHERE a.repository_id::text=$1 AND a.path=$2 FOR UPDATE`, repositoryID, path).Scan(&asset.RepositoryID, &asset.Path, &asset.Digest, &asset.ObjectKey, &asset.Size, &asset.ContentType, &asset.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_raw_asset_tombstones (repository_id,path,digest,content_type,deleted_at) VALUES ($1,$2,$3,$4,now()) ON CONFLICT (repository_id,path) DO UPDATE SET digest=EXCLUDED.digest,content_type=EXCLUDED.content_type,deleted_at=EXCLUDED.deleted_at`, repositoryID, path, asset.Digest, asset.ContentType); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artifact_tombstones (repository_id,format,coordinate,digest) VALUES ($1,'raw',$2,$3) ON CONFLICT (repository_id,format,coordinate) DO UPDATE SET digest=EXCLUDED.digest,tombstoned_at=now()`, repositoryID, path, asset.Digest); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_raw_objects SET created_at=now(),collected_at=NULL WHERE digest=$1`, asset.Digest); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_raw_assets WHERE repository_id::text=$1 AND path=$2`, repositoryID, path); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) RestoreRawAsset(ctx context.Context, repositoryID, path string) (RawAsset, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RawAsset{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var asset RawAsset
	err = tx.QueryRowContext(ctx, `SELECT t.repository_id::text,t.path,t.digest,o.object_key,o.size,t.content_type,t.deleted_at FROM native_raw_asset_tombstones t JOIN native_raw_objects o ON o.digest=t.digest WHERE t.repository_id::text=$1 AND t.path=$2 AND o.collected_at IS NULL FOR UPDATE`, repositoryID, path).Scan(&asset.RepositoryID, &asset.Path, &asset.Digest, &asset.ObjectKey, &asset.Size, &asset.ContentType, &asset.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RawAsset{}, ErrNotFound
	}
	if err != nil {
		return RawAsset{}, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_raw_assets WHERE repository_id::text=$1 AND path=$2)`, repositoryID, path).Scan(&exists); err != nil {
		return RawAsset{}, err
	}
	if exists {
		return RawAsset{}, ErrNameExists
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_raw_assets (repository_id,path,digest,content_type,updated_at) VALUES ($1,$2,$3,$4,now())`, repositoryID, path, asset.Digest, asset.ContentType); err != nil {
		return RawAsset{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_raw_asset_tombstones WHERE repository_id::text=$1 AND path=$2`, repositoryID, path); err != nil {
		return RawAsset{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM artifact_tombstones WHERE repository_id::text=$1 AND format='raw' AND coordinate=$2`, repositoryID, path); err != nil {
		return RawAsset{}, err
	}
	asset.UpdatedAt = time.Now().UTC()
	return asset, tx.Commit()
}
func (s *PostgresStore) ListUnreferencedRawObjects(ctx context.Context, before time.Time, limit int) ([]RawObject, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.repository_id::text,o.digest,o.object_key FROM native_raw_objects o WHERE o.repository_id IS NOT NULL AND o.created_at < $1 AND o.collected_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_raw_assets a WHERE a.digest=o.digest) ORDER BY o.created_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var objects []RawObject
	for rows.Next() {
		var object RawObject
		if err = rows.Scan(&object.RepositoryID, &object.Digest, &object.ObjectKey); err != nil {
			return nil, err
		}
		objects = append(objects, object)
	}
	return objects, rows.Err()
}
func (s *PostgresStore) RawObjectIsUnreferenced(ctx context.Context, digest string) (bool, error) {
	var unreferenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_raw_objects o WHERE o.digest=$1 AND o.collected_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_raw_assets a WHERE a.digest=o.digest))`, digest).Scan(&unreferenced)
	return unreferenced, err
}
func (s *PostgresStore) MarkRawObjectCollected(ctx context.Context, digest string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_raw_objects o SET collected_at=now() WHERE o.digest=$1 AND o.collected_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_raw_assets a WHERE a.digest=o.digest)`, digest)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
