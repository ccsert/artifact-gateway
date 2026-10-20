package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) StageOCIImportObject(ctx context.Context, intent OCIObjectIntent) error {
	result, err := s.db.ExecContext(ctx, `INSERT INTO native_oci_object_intents(object_key,repository_id,digest,size,created_at,claimed_at,collected_at)
VALUES($1,$2,$3,$4,clock_timestamp(),CASE WHEN EXISTS(SELECT 1 FROM native_oci_manifests WHERE object_key=$1) OR EXISTS(SELECT 1 FROM native_oci_blobs WHERE object_key=$1)THEN clock_timestamp() ELSE NULL END,NULL)
ON CONFLICT(object_key)DO UPDATE SET repository_id=COALESCE(native_oci_object_intents.repository_id,EXCLUDED.repository_id),created_at=EXCLUDED.created_at,claimed_at=EXCLUDED.claimed_at,collected_at=NULL
WHERE native_oci_object_intents.digest=EXCLUDED.digest AND native_oci_object_intents.size=EXCLUDED.size`, intent.ObjectKey, intent.RepositoryID, intent.Digest, intent.Size)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrNameExists
	}
	return nil
}

func (s *PostgresStore) LockOCIImport(ctx context.Context, c OCIImportClosure) (context.Context, func(), error) {
	keys := make([]string, 0, len(c.Manifests)+len(c.objects()))
	for _, o := range c.objects() {
		keys = append(keys, "native-oci-object:"+o.ObjectKey)
	}
	return s.lockPostgresAdvisoryKeys(ctx, keys)
}

func (s *PostgresStore) PublishOCIImportClosure(ctx context.Context, c OCIImportClosure) error {
	if err := validateOCIImport(c); err != nil {
		return err
	}
	keys := make([]string, 0, len(c.Manifests))
	for _, m := range c.Manifests {
		keys = append(keys, artifactScanLockKey(c.RepositoryID, FormatOCI, c.Name, m.Digest))
	}
	ctx, release, err := s.lockPostgresAdvisoryKeys(ctx, keys)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = checkNativePublicationGuard(ctx, tx); err != nil {
		return err
	}
	var repoName string
	err = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id::text=$1 AND format='oci' AND repo_type='hosted' AND state='active' FOR SHARE`, c.RepositoryID).Scan(&repoName)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisabled
	}
	if err != nil {
		return err
	}
	changed := false
	for _, m := range c.Manifests {
		var blocked bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_tombstones WHERE repository_id::text=$1 AND format='oci' AND coordinate=$2)`, c.RepositoryID, c.Name+"@"+m.Digest).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked {
			return ErrNameExists
		}
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artifact_quarantines WHERE repository_id::text=$1 AND format='oci' AND coordinate=$2 AND digest=$3 AND state='quarantined')`, c.RepositoryID, c.Name, m.Digest).Scan(&blocked)
		if err != nil {
			return err
		}
		if blocked {
			return ErrArtifactQuarantined
		}
		var old OCIManifest
		err = tx.QueryRowContext(ctx, `SELECT object_key,size,media_type FROM native_oci_manifests WHERE repository_id::text=$1 AND name=$2 AND digest=$3 FOR SHARE`, c.RepositoryID, c.Name, m.Digest).Scan(&old.ObjectKey, &old.Size, &old.MediaType)
		if err == nil {
			if old.ObjectKey != m.ObjectKey || old.Size != m.Size || old.MediaType != m.MediaType {
				return ErrNameExists
			}
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if m.ObjectKey != canonicalOCIImportManifestKey(c, m.Digest) {
			return ErrDisabled
		}
		if err = checkOCIImportIntent(ctx, tx, m.ObjectKey, m.Digest, m.Size); err != nil {
			return err
		}
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO native_oci_manifests(repository_id,name,digest,object_key,media_type,size)VALUES($1,$2,$3,$4,$5,$6)ON CONFLICT(repository_id,name,digest)DO NOTHING`, c.RepositoryID, c.Name, m.Digest, m.ObjectKey, m.MediaType, m.Size)
		if insertErr != nil {
			return insertErr
		}
		n, _ := result.RowsAffected()
		changed = changed || n > 0
	}
	for _, b := range c.Blobs {
		var old OCIBlob
		err = tx.QueryRowContext(ctx, `SELECT object_key,size FROM native_oci_blobs WHERE digest=$1 FOR SHARE`, b.Digest).Scan(&old.ObjectKey, &old.Size)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil && (old.ObjectKey != b.ObjectKey || old.Size != b.Size) {
			return ErrNameExists
		}
		if errors.Is(err, sql.ErrNoRows) {
			if err = checkOCIImportIntent(ctx, tx, b.ObjectKey, b.Digest, b.Size); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO native_oci_blobs(digest,object_key,size)VALUES($1,$2,$3)ON CONFLICT(digest)DO NOTHING`, b.Digest, b.ObjectKey, b.Size); err != nil {
				return err
			}
		}
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO native_oci_repository_blobs(repository_id,digest)VALUES($1,$2)ON CONFLICT DO NOTHING`, c.RepositoryID, b.Digest)
		if insertErr != nil {
			return insertErr
		}
		n, _ := result.RowsAffected()
		changed = changed || n > 0
	}
	if c.Reference != c.RootDigest {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO native_oci_tags(repository_id,name,tag,digest)VALUES($1,$2,$3,$4)ON CONFLICT DO NOTHING`, c.RepositoryID, c.Name, c.Reference, c.RootDigest)
		if insertErr != nil {
			return insertErr
		}
		n, _ := result.RowsAffected()
		changed = changed || n > 0
		var current string
		if err = tx.QueryRowContext(ctx, `SELECT digest FROM native_oci_tags WHERE repository_id::text=$1 AND name=$2 AND tag=$3 FOR UPDATE`, c.RepositoryID, c.Name, c.Reference).Scan(&current); err != nil {
			return err
		}
		if current != c.RootDigest {
			return ErrNameExists
		}
	}
	for _, o := range c.objects() {
		if _, err = tx.ExecContext(ctx, `UPDATE native_oci_object_intents SET claimed_at=now() WHERE object_key=$1 AND digest=$2 AND size=$3 AND collected_at IS NULL`, o.ObjectKey, o.Digest, o.Size); err != nil {
			return err
		}
	}
	if changed {
		if err = insertAudit(ctx, tx, ociImportAudit(c, repoName)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func checkOCIImportIntent(ctx context.Context, tx *sql.Tx, key, digest string, size int64) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM native_oci_object_intents WHERE object_key=$1 AND digest=$2 AND size=$3 AND collected_at IS NULL)`, key, digest, size).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}
