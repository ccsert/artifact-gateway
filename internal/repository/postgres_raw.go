package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const rawUploadColumns = "id::text,repository_id::text,path,object_key,byte_offset,state,expires_at,collected_at"

func scanRawUpload(scanner interface{ Scan(...any) error }, upload *RawUpload) error {
	var collectedAt sql.NullTime
	if err := scanner.Scan(&upload.ID, &upload.RepositoryID, &upload.Path, &upload.ObjectKey, &upload.Offset, &upload.State, &upload.ExpiresAt, &collectedAt); err != nil {
		return err
	}
	if collectedAt.Valid {
		upload.CollectedAt = collectedAt.Time
	}
	return nil
}

func prefixedRawUploadColumns(prefix string) string {
	return prefix + ".id::text," + prefix + ".repository_id::text," + prefix + ".path," + prefix + ".object_key," + prefix + ".byte_offset," + prefix + ".state," + prefix + ".expires_at," + prefix + ".collected_at"
}

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
	err := scanRawUpload(s.db.QueryRowContext(ctx, `SELECT `+rawUploadColumns+` FROM native_raw_uploads WHERE id::text=$1`, id), &v)
	if errors.Is(err, sql.ErrNoRows) {
		return RawUpload{}, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) UpdateRawUpload(ctx context.Context, id string, offset int64) (RawUpload, error) {
	var v RawUpload
	err := scanRawUpload(s.db.QueryRowContext(ctx, `UPDATE native_raw_uploads SET byte_offset=$2 WHERE id::text=$1 AND state='open' AND expires_at>now() RETURNING `+rawUploadColumns, id, offset), &v)
	if errors.Is(err, sql.ErrNoRows) {
		return RawUpload{}, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) CancelRawUpload(ctx context.Context, id string) (RawUpload, error) {
	var v RawUpload
	err := scanRawUpload(s.db.QueryRowContext(ctx, `UPDATE native_raw_uploads SET state='cancelled' WHERE id::text=$1 AND state='open' RETURNING `+rawUploadColumns, id), &v)
	if errors.Is(err, sql.ErrNoRows) {
		return RawUpload{}, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) CompleteRawUpload(ctx context.Context, id string, asset RawAsset) (RawAsset, error) {
	return s.CompleteRawUploadIf(ctx, id, asset, RawWriteCondition{})
}

// CompleteRawUploadIf publishes an open upload only when the path's current
// reference satisfies condition. A failed condition rolls back and leaves the
// upload open.
func (s *PostgresStore) CompleteRawUploadIf(ctx context.Context, id string, asset RawAsset, condition RawWriteCondition) (RawAsset, error) {
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
	if current, replaceErr := replaceRawAssetIf(ctx, tx, asset, condition); replaceErr != nil {
		if errors.Is(replaceErr, ErrPreconditionFailed) {
			return current, replaceErr
		}
		return asset, replaceErr
	}
	asset.UpdatedAt = time.Now().UTC()
	if _, err = tx.ExecContext(ctx, `UPDATE native_raw_uploads SET state='completed' WHERE id::text=$1`, id); err != nil {
		return asset, err
	}
	return asset, tx.Commit()
}

func (s *PostgresStore) ExpireRawUploads(ctx context.Context, before time.Time, limit int) ([]RawUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `WITH candidates AS (
        SELECT id FROM native_raw_uploads
        WHERE state='open' AND expires_at < $1
        ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT $2
    ) UPDATE native_raw_uploads u SET state='cancelled'
    FROM candidates c WHERE u.id=c.id
	RETURNING `+prefixedRawUploadColumns("u"), before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	uploads := make([]RawUpload, 0)
	for rows.Next() {
		var upload RawUpload
		if err = scanRawUpload(rows, &upload); err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return uploads, tx.Commit()
}

func (s *PostgresStore) ListUncollectedRawUploads(ctx context.Context, limit int) ([]RawUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+rawUploadColumns+` FROM native_raw_uploads WHERE state IN ('completed','cancelled') AND collected_at IS NULL ORDER BY expires_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	uploads := make([]RawUpload, 0)
	for rows.Next() {
		var upload RawUpload
		if err = scanRawUpload(rows, &upload); err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	return uploads, rows.Err()
}

func (s *PostgresStore) MarkRawUploadCollected(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_raw_uploads SET collected_at=now() WHERE id::text=$1 AND state IN ('completed','cancelled') AND collected_at IS NULL`, id)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) PutRawAsset(ctx context.Context, v RawAsset) (RawAsset, error) {
	return s.PutRawAssetIf(ctx, v, RawWriteCondition{})
}

// PutRawAssetIf replaces the path reference only when its current state
// satisfies condition; on ErrPreconditionFailed it returns the current asset.
func (s *PostgresStore) PutRawAssetIf(ctx context.Context, v RawAsset, condition RawWriteCondition) (RawAsset, error) {
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
	if current, replaceErr := replaceRawAssetIf(ctx, tx, v, condition); replaceErr != nil {
		if errors.Is(replaceErr, ErrPreconditionFailed) {
			return current, replaceErr
		}
		return v, replaceErr
	}
	v.UpdatedAt = time.Now().UTC()
	return v, tx.Commit()
}

// replaceRawAssetIf points the path at asset inside tx. A conditional write
// locks the current reference row before evaluating condition, so a competing
// writer either waits for this transaction or is evaluated against its result.
// An absent path is created with ON CONFLICT DO NOTHING; losing that race
// re-evaluates the condition against the reference the winner committed.
func replaceRawAssetIf(ctx context.Context, tx *sql.Tx, asset RawAsset, condition RawWriteCondition) (RawAsset, error) {
	if condition.Unconditional() {
		_, err := tx.ExecContext(ctx, `INSERT INTO native_raw_assets (repository_id,path,digest,content_type) VALUES ($1,$2,$3,$4) ON CONFLICT (repository_id,path) DO UPDATE SET digest=EXCLUDED.digest,content_type=EXCLUDED.content_type,updated_at=now()`, asset.RepositoryID, asset.Path, asset.Digest, asset.ContentType)
		return RawAsset{}, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		current := RawAsset{RepositoryID: asset.RepositoryID, Path: asset.Path}
		err := tx.QueryRowContext(ctx, `SELECT digest,content_type,updated_at FROM native_raw_assets WHERE repository_id=$1::uuid AND path=$2 FOR UPDATE`, asset.RepositoryID, asset.Path).Scan(&current.Digest, &current.ContentType, &current.UpdatedAt)
		exists := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return RawAsset{}, err
		}
		if !condition.Allows(current, exists) {
			return current, ErrPreconditionFailed
		}
		if exists {
			_, err = tx.ExecContext(ctx, `UPDATE native_raw_assets SET digest=$3,content_type=$4,updated_at=now() WHERE repository_id=$1::uuid AND path=$2`, asset.RepositoryID, asset.Path, asset.Digest, asset.ContentType)
			return RawAsset{}, err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO native_raw_assets (repository_id,path,digest,content_type) VALUES ($1,$2,$3,$4) ON CONFLICT (repository_id,path) DO NOTHING`, asset.RepositoryID, asset.Path, asset.Digest, asset.ContentType)
		if err != nil {
			return RawAsset{}, err
		}
		if inserted, _ := result.RowsAffected(); inserted == 1 {
			return RawAsset{}, nil
		}
	}
	return RawAsset{}, ErrPreconditionFailed
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
func (s *PostgresStore) ListRawAssets(ctx context.Context, repositoryID, prefix string, limit int, after string, matches ...ArtifactSearchMatch) ([]RawAsset, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.repository_id::text,a.path,a.digest,o.object_key,o.size,a.content_type,a.updated_at FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest WHERE a.repository_id=$1::uuid AND ($2='' OR `+searchTextSQL("a.path", "$2", matches)+`) AND a.path>$3 ORDER BY a.path LIMIT $4`, repositoryID, searchTextPattern(prefix, matches), after, limit)
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
