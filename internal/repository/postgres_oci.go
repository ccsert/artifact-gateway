package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *PostgresStore) CreateOCIUpload(ctx context.Context, v OCIUpload) (OCIUpload, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO native_oci_uploads (id,repository_id,name,object_key,byte_offset,state,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, v.ID, v.RepositoryID, v.Name, v.ObjectKey, v.Offset, v.State, v.ExpiresAt)
	return v, err
}
func (s *PostgresStore) StageOCIObjectIntent(ctx context.Context, intent OCIObjectIntent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO native_oci_object_intents (object_key,repository_id,digest,size) VALUES ($1,$2,$3,$4) ON CONFLICT (object_key) DO UPDATE SET repository_id=COALESCE(native_oci_object_intents.repository_id,EXCLUDED.repository_id)`, intent.ObjectKey, nullableString(intent.RepositoryID), intent.Digest, intent.Size)
	return err
}

// LockOCIUpload holds a PostgreSQL session advisory lock, rather than a
// transaction lock, because the protected operation also includes MinIO I/O.
// The caller must invoke the returned release function exactly once.
func (s *PostgresStore) LockOCIUpload(ctx context.Context, id string) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, "native-oci-upload:"+id); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "native-oci-upload:"+id)
		_ = conn.Close()
	}, nil
}

// LockOCIObject serializes object publication with object-intent collection
// across gateway instances. The interval includes object-store I/O.
func (s *PostgresStore) LockOCIObject(ctx context.Context, objectKey string) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, "native-oci-object:"+objectKey); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "native-oci-object:"+objectKey)
		_ = conn.Close()
	}, nil
}
func (s *PostgresStore) GetOCIUpload(ctx context.Context, id string) (OCIUpload, error) {
	var v OCIUpload
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,name,object_key,byte_offset,state,expires_at FROM native_oci_uploads WHERE id::text=$1`, id).Scan(&v.ID, &v.RepositoryID, &v.Name, &v.ObjectKey, &v.Offset, &v.State, &v.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OCIUpload{}, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) UpdateOCIUpload(ctx context.Context, id string, offset int64) (OCIUpload, error) {
	var v OCIUpload
	err := s.db.QueryRowContext(ctx, `UPDATE native_oci_uploads SET byte_offset=$2 WHERE id::text=$1 AND state='open' AND expires_at > now() RETURNING id::text,repository_id::text,name,object_key,byte_offset,state,expires_at`, id, offset).Scan(&v.ID, &v.RepositoryID, &v.Name, &v.ObjectKey, &v.Offset, &v.State, &v.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OCIUpload{}, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) CancelOCIUpload(ctx context.Context, id string) (OCIUpload, error) {
	var v OCIUpload
	err := s.db.QueryRowContext(ctx, `UPDATE native_oci_uploads
        SET state='expired', collected_at=now()
        WHERE id::text=$1 AND state='open'
        RETURNING id::text,repository_id::text,name,object_key,byte_offset,state,expires_at,collected_at`, id).Scan(&v.ID, &v.RepositoryID, &v.Name, &v.ObjectKey, &v.Offset, &v.State, &v.ExpiresAt, &v.CollectedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return OCIUpload{}, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) CompleteOCIUpload(ctx context.Context, id string, blob OCIBlob) (OCIBlob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OCIBlob{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var repositoryID string
	err = tx.QueryRowContext(ctx, `SELECT repository_id::text FROM native_oci_uploads WHERE id::text=$1 AND state='open' AND expires_at > now() FOR UPDATE`, id).Scan(&repositoryID)
	if errors.Is(err, sql.ErrNoRows) {
		return OCIBlob{}, ErrNotFound
	}
	if err != nil {
		return OCIBlob{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_oci_blobs (digest,object_key,size) VALUES ($1,$2,$3) ON CONFLICT (digest) DO NOTHING`, blob.Digest, blob.ObjectKey, blob.Size); err != nil {
		return OCIBlob{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT digest,object_key,size FROM native_oci_blobs WHERE digest=$1`, blob.Digest).Scan(&blob.Digest, &blob.ObjectKey, &blob.Size); err != nil {
		return OCIBlob{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_oci_repository_blobs (repository_id,digest) VALUES ($1,$2) ON CONFLICT DO NOTHING`, repositoryID, blob.Digest); err != nil {
		return OCIBlob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_oci_object_intents SET claimed_at=now() WHERE object_key=$1 AND claimed_at IS NULL`, blob.ObjectKey); err != nil {
		return OCIBlob{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_oci_uploads SET state='completed' WHERE id::text=$1`, id); err != nil {
		return OCIBlob{}, err
	}
	return blob, tx.Commit()
}
func (s *PostgresStore) ExpireOCIUploads(ctx context.Context, before time.Time, limit int) ([]OCIUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `WITH candidates AS (
        SELECT id FROM native_oci_uploads
        WHERE state='open' AND expires_at < $1
        ORDER BY expires_at FOR UPDATE SKIP LOCKED LIMIT $2
    ) UPDATE native_oci_uploads u SET state='expired'
    FROM candidates c WHERE u.id=c.id
    RETURNING u.id::text,u.repository_id::text,u.name,u.object_key,u.byte_offset,u.state,u.expires_at,u.collected_at`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var uploads []OCIUpload
	for rows.Next() {
		var upload OCIUpload
		if err = rows.Scan(&upload.ID, &upload.RepositoryID, &upload.Name, &upload.ObjectKey, &upload.Offset, &upload.State, &upload.ExpiresAt, &upload.CollectedAt); err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return uploads, tx.Commit()
}
func (s *PostgresStore) ListUncollectedOCIUploads(ctx context.Context, limit int) ([]OCIUpload, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,repository_id::text,name,object_key,byte_offset,state,expires_at,collected_at FROM native_oci_uploads WHERE state='expired' AND collected_at IS NULL ORDER BY expires_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var uploads []OCIUpload
	for rows.Next() {
		var upload OCIUpload
		if err = rows.Scan(&upload.ID, &upload.RepositoryID, &upload.Name, &upload.ObjectKey, &upload.Offset, &upload.State, &upload.ExpiresAt, &upload.CollectedAt); err != nil {
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	return uploads, rows.Err()
}
func (s *PostgresStore) MarkOCIUploadCollected(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_oci_uploads SET collected_at=now() WHERE id::text=$1 AND state='expired' AND collected_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) ListUnclaimedOCIObjectIntents(ctx context.Context, before time.Time, limit int) ([]OCIObjectIntent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT repository_id::text,object_key,digest,size,created_at,claimed_at,collected_at FROM native_oci_object_intents WHERE repository_id IS NOT NULL AND created_at < $1 AND claimed_at IS NULL AND collected_at IS NULL ORDER BY created_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var intents []OCIObjectIntent
	for rows.Next() {
		var intent OCIObjectIntent
		var claimedAt, collectedAt sql.NullTime
		if err = rows.Scan(&intent.RepositoryID, &intent.ObjectKey, &intent.Digest, &intent.Size, &intent.CreatedAt, &claimedAt, &collectedAt); err != nil {
			return nil, err
		}
		if claimedAt.Valid {
			intent.ClaimedAt = claimedAt.Time
		}
		if collectedAt.Valid {
			intent.CollectedAt = collectedAt.Time
		}
		intents = append(intents, intent)
	}
	return intents, rows.Err()
}
func (s *PostgresStore) OCIObjectIntentIsUnclaimed(ctx context.Context, objectKey string) (bool, error) {
	var unclaimed bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_oci_object_intents WHERE object_key=$1 AND claimed_at IS NULL AND collected_at IS NULL)`, objectKey).Scan(&unclaimed)
	return unclaimed, err
}
func (s *PostgresStore) MarkOCIObjectIntentCollected(ctx context.Context, objectKey string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_oci_object_intents SET collected_at=now() WHERE object_key=$1 AND claimed_at IS NULL AND collected_at IS NULL`, objectKey)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) MountOCIBlob(ctx context.Context, repositoryID, digest string) (OCIBlob, error) {
	var v OCIBlob
	err := s.db.QueryRowContext(ctx, `SELECT digest,object_key,size FROM native_oci_blobs WHERE digest=$1`, digest).Scan(&v.Digest, &v.ObjectKey, &v.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO native_oci_repository_blobs (repository_id,digest) VALUES ($1,$2) ON CONFLICT DO NOTHING`, repositoryID, digest)
	return v, err
}
func (s *PostgresStore) MountOCIBlobFrom(ctx context.Context, repositoryID, sourceRepositoryID, digest string) (OCIBlob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OCIBlob{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var v OCIBlob
	err = tx.QueryRowContext(ctx, `SELECT b.digest,b.object_key,b.size
        FROM native_oci_blobs b
        JOIN native_oci_repository_blobs source ON source.digest=b.digest
        WHERE source.repository_id::text=$1 AND b.digest=$2
        FOR SHARE`, sourceRepositoryID, digest).Scan(&v.Digest, &v.ObjectKey, &v.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return OCIBlob{}, ErrNotFound
	}
	if err != nil {
		return OCIBlob{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_oci_repository_blobs (repository_id,digest) VALUES ($1,$2) ON CONFLICT DO NOTHING`, repositoryID, digest); err != nil {
		return OCIBlob{}, err
	}
	if err = tx.Commit(); err != nil {
		return OCIBlob{}, err
	}
	return v, nil
}
func (s *PostgresStore) GetOCIBlob(ctx context.Context, repositoryID, digest string) (OCIBlob, error) {
	var v OCIBlob
	err := s.db.QueryRowContext(ctx, `SELECT b.digest,b.object_key,b.size FROM native_oci_blobs b JOIN native_oci_repository_blobs rb ON rb.digest=b.digest WHERE rb.repository_id::text=$1 AND b.digest=$2`, repositoryID, digest).Scan(&v.Digest, &v.ObjectKey, &v.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) PutOCIManifest(ctx context.Context, v OCIManifest, reference string) (OCIManifest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_oci_manifests (repository_id,name,digest,object_key,media_type,size,subject_digest,artifact_type) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (repository_id,name,digest) DO NOTHING`, v.RepositoryID, v.Name, v.Digest, v.ObjectKey, v.MediaType, v.Size, nullableString(v.SubjectDigest), v.ArtifactType); err != nil {
		return v, err
	}
	if !strings.HasPrefix(reference, "sha256:") {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_oci_tags (repository_id,name,tag,digest) VALUES ($1,$2,$3,$4) ON CONFLICT (repository_id,name,tag) DO UPDATE SET digest=EXCLUDED.digest,updated_at=now()`, v.RepositoryID, v.Name, reference, v.Digest); err != nil {
			return v, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_oci_object_intents SET claimed_at=now() WHERE object_key=$1 AND claimed_at IS NULL`, v.ObjectKey); err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func (s *PostgresStore) GetOCIManifest(ctx context.Context, repositoryID, name, reference string) (OCIManifest, error) {
	var v OCIManifest
	query := `SELECT repository_id::text,name,digest,object_key,media_type,size FROM native_oci_manifests WHERE repository_id::text=$1 AND name=$2 AND digest=$3`
	args := []any{repositoryID, name, reference}
	if !strings.HasPrefix(reference, "sha256:") {
		query = `SELECT m.repository_id::text,m.name,m.digest,m.object_key,m.media_type,m.size FROM native_oci_tags t JOIN native_oci_manifests m ON (m.repository_id=t.repository_id AND m.name=t.name AND m.digest=t.digest) WHERE t.repository_id::text=$1 AND t.name=$2 AND t.tag=$3`
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&v.RepositoryID, &v.Name, &v.Digest, &v.ObjectKey, &v.MediaType, &v.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) ListOCIReferrers(ctx context.Context, repositoryID, name, subject string, limit int, after string) ([]OCIManifest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT repository_id::text,name,digest,object_key,media_type,size,subject_digest,artifact_type FROM native_oci_manifests WHERE repository_id::text=$1 AND name=$2 AND subject_digest=$3 AND digest>$4 ORDER BY digest LIMIT $5`, repositoryID, name, subject, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OCIManifest
	for rows.Next() {
		var v OCIManifest
		if err := rows.Scan(&v.RepositoryID, &v.Name, &v.Digest, &v.ObjectKey, &v.MediaType, &v.Size, &v.SubjectDigest, &v.ArtifactType); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *PostgresStore) ListOCITags(ctx context.Context, repositoryID, name string, limit int, after string) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT tag FROM native_oci_tags
        WHERE repository_id::text=$1 AND name=$2 AND ($3='' OR tag > $3)
        ORDER BY tag LIMIT $4`, repositoryID, name, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}
func (s *PostgresStore) DeleteOCIManifest(ctx context.Context, repositoryID, name, digest string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_oci_tags WHERE repository_id::text=$1 AND name=$2 AND digest=$3`, repositoryID, name, digest); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_oci_object_intents (object_key,repository_id,digest,size,created_at,claimed_at,collected_at) SELECT object_key,repository_id,digest,size,now(),NULL,NULL FROM native_oci_manifests WHERE repository_id::text=$1 AND name=$2 AND digest=$3 ON CONFLICT (object_key) DO UPDATE SET repository_id=EXCLUDED.repository_id,claimed_at=NULL,collected_at=NULL,created_at=EXCLUDED.created_at`, repositoryID, name, digest); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artifact_tombstones (repository_id,format,coordinate,digest) SELECT repository_id,'oci',name || '@' || digest,digest FROM native_oci_manifests WHERE repository_id::text=$1 AND name=$2 AND digest=$3 ON CONFLICT DO NOTHING`, repositoryID, name, digest); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM native_oci_manifests WHERE repository_id::text=$1 AND name=$2 AND digest=$3`, repositoryID, name, digest)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *PostgresStore) GetArtifactTombstone(ctx context.Context, repositoryID string, format Format, coordinate string) (ArtifactTombstone, error) {
	var tombstone ArtifactTombstone
	err := s.db.QueryRowContext(ctx, `SELECT repository_id::text,format,coordinate,digest,tombstoned_at FROM artifact_tombstones WHERE repository_id::text=$1 AND format=$2 AND coordinate=$3`, repositoryID, format, coordinate).Scan(&tombstone.RepositoryID, &tombstone.Format, &tombstone.Coordinate, &tombstone.Digest, &tombstone.TombstonedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtifactTombstone{}, ErrNotFound
	}
	return tombstone, err
}
