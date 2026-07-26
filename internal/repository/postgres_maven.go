package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *PostgresStore) CreateMavenPublishSession(ctx context.Context, v MavenPublishSession) (MavenPublishSession, error) {
	objects, _ := json.Marshal(v.Objects)
	_, err := s.db.ExecContext(ctx, `INSERT INTO native_maven_publish_sessions (id,repository_id,coordinate,publisher,pom_object,state,expires_at,objects) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, v.ID, v.RepositoryID, v.Coordinate, v.Publisher, v.PomObject, v.State, v.ExpiresAt, objects)
	return v, err
}
func (s *PostgresStore) CreateMavenPublishSessionIdempotently(ctx context.Context, v MavenPublishSession, actor, target, key, payload string) (MavenPublishSession, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenPublishSession{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Remove an expired key under the same transaction before attempting the
	// insert. This keeps the primary key reusable after its 24h replay window.
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_maven_publish_idempotency WHERE actor=$1 AND target=$2 AND key=$3 AND expires_at <= now()`, actor, target, key); err != nil {
		return MavenPublishSession{}, false, err
	}
	var existingID, existingPayload string
	err = tx.QueryRowContext(ctx, `SELECT session_id::text,payload_hash FROM native_maven_publish_idempotency WHERE actor=$1 AND target=$2 AND key=$3 FOR UPDATE`, actor, target, key).Scan(&existingID, &existingPayload)
	if err == nil {
		if existingPayload != payload {
			return MavenPublishSession{}, false, ErrIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return MavenPublishSession{}, false, err
		}
		existing, err := s.GetMavenPublishSession(ctx, existingID)
		return existing, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MavenPublishSession{}, false, err
	}
	objects, _ := json.Marshal(v.Objects)
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_publish_sessions (id,repository_id,coordinate,publisher,pom_object,state,expires_at,objects) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, v.ID, v.RepositoryID, v.Coordinate, v.Publisher, v.PomObject, v.State, v.ExpiresAt, objects); err != nil {
		return MavenPublishSession{}, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO native_maven_publish_idempotency (actor,target,key,payload_hash,session_id,expires_at) VALUES ($1,$2,$3,$4,$5,now()+interval '24 hours') ON CONFLICT (actor,target,key) DO NOTHING`, actor, target, key, payload, v.ID)
	if err != nil {
		return MavenPublishSession{}, false, err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		// A concurrent creator won. Its committed record is now authoritative;
		// discard our staged session along with this transaction and replay it.
		if err = tx.QueryRowContext(ctx, `SELECT session_id::text,payload_hash FROM native_maven_publish_idempotency WHERE actor=$1 AND target=$2 AND key=$3 FOR UPDATE`, actor, target, key).Scan(&existingID, &existingPayload); err != nil {
			return MavenPublishSession{}, false, err
		}
		if existingPayload != payload {
			return MavenPublishSession{}, false, ErrIdempotencyConflict
		}
		if err = tx.Rollback(); err != nil {
			return MavenPublishSession{}, false, err
		}
		existing, getErr := s.GetMavenPublishSession(ctx, existingID)
		return existing, true, getErr
	}
	if err = tx.Commit(); err != nil {
		return MavenPublishSession{}, false, err
	}
	return v, false, nil
}
func (s *PostgresStore) GetMavenPublishSession(ctx context.Context, id string) (MavenPublishSession, error) {
	var v MavenPublishSession
	var objects []byte
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,publisher,pom_object,state,expires_at,objects FROM native_maven_publish_sessions WHERE id::text=$1`, id).Scan(&v.ID, &v.RepositoryID, &v.Coordinate, &v.Publisher, &v.PomObject, &v.State, &v.ExpiresAt, &objects)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(objects, &v.Objects)
	}
	return v, err
}
func (s *PostgresStore) FindOpenMavenPublishSession(ctx context.Context, repoID, coordinate, publisher string) (MavenPublishSession, error) {
	var v MavenPublishSession
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,publisher,pom_object,state,expires_at,objects FROM native_maven_publish_sessions WHERE repository_id::text=$1 AND coordinate=$2 AND publisher=$3 AND state='open'`, repoID, coordinate, publisher).Scan(&v.ID, &v.RepositoryID, &v.Coordinate, &v.Publisher, &v.PomObject, &v.State, &v.ExpiresAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(raw, &v.Objects)
}
func (s *PostgresStore) FindMavenPublishSession(ctx context.Context, repoID, coordinate, publisher string) (MavenPublishSession, error) {
	var v MavenPublishSession
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,publisher,pom_object,state,expires_at,objects FROM native_maven_publish_sessions WHERE repository_id::text=$1 AND coordinate=$2 AND publisher=$3 ORDER BY CASE state WHEN 'open' THEN 0 ELSE 1 END, expires_at DESC LIMIT 1`, repoID, coordinate, publisher).Scan(&v.ID, &v.RepositoryID, &v.Coordinate, &v.Publisher, &v.PomObject, &v.State, &v.ExpiresAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(raw, &v.Objects)
}
func (s *PostgresStore) FindAnyMavenPublishSession(ctx context.Context, repoID, coordinate string) (MavenPublishSession, error) {
	var v MavenPublishSession
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,publisher,pom_object,state,expires_at,objects FROM native_maven_publish_sessions WHERE repository_id::text=$1 AND coordinate=$2 ORDER BY CASE state WHEN 'open' THEN 0 ELSE 1 END, expires_at DESC LIMIT 1`, repoID, coordinate).Scan(&v.ID, &v.RepositoryID, &v.Coordinate, &v.Publisher, &v.PomObject, &v.State, &v.ExpiresAt, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(raw, &v.Objects)
}
func (s *PostgresStore) AppendMavenPublishObject(ctx context.Context, id string, object MavenDeclaredObject) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT objects FROM native_maven_publish_sessions WHERE id::text=$1 AND state='open' FOR UPDATE`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var objects []MavenDeclaredObject
	if err = json.Unmarshal(raw, &objects); err != nil {
		return err
	}
	for _, o := range objects {
		if o.Name == object.Name {
			if o.Digest != object.Digest || o.Size != object.Size {
				return ErrNameExists
			}
			return tx.Commit()
		}
	}
	objects = append(objects, object)
	raw, _ = json.Marshal(objects)
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET objects=$2 WHERE id::text=$1`, id, raw); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *PostgresStore) SetMavenPublishPom(ctx context.Context, id, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET pom_object=$2 WHERE id::text=$1 AND state='open'`, id, name)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) MarkMavenPublishObject(ctx context.Context, id, name, key string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw []byte
	if err = tx.QueryRowContext(ctx, `SELECT objects FROM native_maven_publish_sessions WHERE id::text=$1 AND state='open' FOR UPDATE`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var objects []MavenDeclaredObject
	if err := json.Unmarshal(raw, &objects); err != nil {
		return err
	}
	var declared *MavenDeclaredObject
	for i := range objects {
		if objects[i].Name == name {
			declared = &objects[i]
			break
		}
	}
	if declared == nil {
		return ErrNotFound
	}
	var claimed, deleted bool
	err = tx.QueryRowContext(ctx, `SELECT claimed_at IS NOT NULL, deleted_at IS NOT NULL FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, key).Scan(&claimed, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_intents (object_key,session_id,digest,size) VALUES ($1,$2,$3,$4)`, key, id, declared.Digest, declared.Size); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if claimed && !deleted {
		return ErrDisabled
	} else if deleted {
		if _, err = tx.ExecContext(ctx, `UPDATE native_maven_object_intents SET session_id=$2,digest=$3,size=$4,created_at=now(),claimed_at=NULL,claimed_token=NULL,deleted_at=NULL WHERE object_key=$1`, key, id, declared.Digest, declared.Size); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO native_maven_publish_uploads (session_id,object_name,object_key) VALUES ($1,$2,$3) ON CONFLICT (session_id,object_name) DO UPDATE SET object_key=EXCLUDED.object_key`, id, name, key)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}
func (s *PostgresStore) CommitMavenPublishSession(ctx context.Context, id string, assets []MavenAsset) (MavenArtifact, error) {
	artifact, _, err := s.commitMavenPublishSession(ctx, id, "", "", assets)
	return artifact, err
}

func (s *PostgresStore) CommitMavenPublishSessionIdempotently(ctx context.Context, id, key, payload string, assets []MavenAsset) (MavenArtifact, bool, error) {
	return s.commitMavenPublishSession(ctx, id, key, payload, assets)
}

func (s *PostgresStore) commitMavenPublishSession(ctx context.Context, id, key, payload string, assets []MavenAsset) (MavenArtifact, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenArtifact{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	var v MavenPublishSession
	var objects []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,publisher,pom_object,state,expires_at,objects FROM native_maven_publish_sessions WHERE id::text=$1 FOR UPDATE`, id).Scan(&v.ID, &v.RepositoryID, &v.Coordinate, &v.Publisher, &v.PomObject, &v.State, &v.ExpiresAt, &objects)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, false, ErrNotFound
	}
	if err != nil {
		return MavenArtifact{}, false, err
	}
	if key != "" {
		if _, err = tx.ExecContext(ctx, `DELETE FROM native_maven_commit_idempotency WHERE session_id=$1 AND expires_at <= now()`, id); err != nil {
			return MavenArtifact{}, false, err
		}
		var storedKey, storedPayload string
		recordErr := tx.QueryRowContext(ctx, `SELECT key,payload_hash FROM native_maven_commit_idempotency WHERE session_id=$1`, id).Scan(&storedKey, &storedPayload)
		if recordErr == nil {
			if storedKey != key || storedPayload != payload {
				return MavenArtifact{}, false, ErrIdempotencyConflict
			}
			if v.State != "committed" {
				return MavenArtifact{}, false, ErrDisabled
			}
			var artifact MavenArtifact
			if err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at FROM native_maven_artifacts WHERE id=$1`, id).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt); err != nil {
				return MavenArtifact{}, false, err
			}
			if err = tx.Commit(); err != nil {
				return MavenArtifact{}, false, err
			}
			return artifact, true, nil
		}
		if !errors.Is(recordErr, sql.ErrNoRows) {
			return MavenArtifact{}, false, recordErr
		}
		if v.State == "committed" {
			return MavenArtifact{}, false, ErrNameExists
		}
	}
	if v.State != "open" || time.Now().After(v.ExpiresAt) {
		return MavenArtifact{}, false, ErrDisabled
	}
	if err = json.Unmarshal(objects, &v.Objects); err != nil {
		return MavenArtifact{}, false, err
	}
	var uploaded int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_publish_uploads WHERE session_id=$1`, id).Scan(&uploaded); err != nil {
		return MavenArtifact{}, false, err
	}
	if uploaded != len(v.Objects) {
		return MavenArtifact{}, false, ErrDisabled
	}
	var claimed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_maven_object_intents WHERE session_id=$1 AND claimed_at IS NOT NULL)`, id).Scan(&claimed); err != nil {
		return MavenArtifact{}, false, err
	}
	if claimed {
		return MavenArtifact{}, false, ErrDisabled
	}
	for _, a := range assets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_intents (object_key,session_id,digest,size) VALUES ($1,$2,$3,$4) ON CONFLICT (object_key) DO NOTHING`, a.ObjectKey, id, a.Digest, a.Size); err != nil {
			return MavenArtifact{}, false, err
		}
		var intentClaimed, intentDeleted bool
		if err = tx.QueryRowContext(ctx, `SELECT claimed_at IS NOT NULL, deleted_at IS NOT NULL FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, a.ObjectKey).Scan(&intentClaimed, &intentDeleted); err != nil {
			return MavenArtifact{}, false, err
		}
		if intentClaimed || intentDeleted {
			return MavenArtifact{}, false, ErrDisabled
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO native_maven_assets (repository_id,path,object_key,digest,size) VALUES ($1,$2,$3,$4,$5)`, a.RepositoryID, a.Path, a.ObjectKey, a.Digest, a.Size)
		if isUnique(err) {
			return MavenArtifact{}, false, ErrNameExists
		}
		if err != nil {
			return MavenArtifact{}, false, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) VALUES ($1,$2) ON CONFLICT (object_key) DO NOTHING`, a.ObjectKey, a.RepositoryID); err != nil {
			return MavenArtifact{}, false, err
		}
	}
	a := MavenArtifact{ID: id, RepositoryID: v.RepositoryID, Coordinate: v.Coordinate, Digest: v.Objects[0].Digest, State: "visible", CreatedAt: time.Now().UTC()}
	err = tx.QueryRowContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state) VALUES ($1,$2,$3,$4,'visible') ON CONFLICT (repository_id,coordinate) DO NOTHING RETURNING id::text, digest, state, created_at`, a.ID, a.RepositoryID, a.Coordinate, a.Digest).Scan(&a.ID, &a.Digest, &a.State, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, false, ErrNameExists
	}
	if err != nil {
		return MavenArtifact{}, false, err
	}
	if key != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_commit_idempotency (session_id,key,payload_hash,expires_at) VALUES ($1,$2,$3,now()+interval '24 hours')`, id, key, payload); err != nil {
			return MavenArtifact{}, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET state='committed' WHERE id=$1`, id); err != nil {
		return MavenArtifact{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return MavenArtifact{}, false, err
	}
	return a, false, nil
}
func (s *PostgresStore) GetMavenAsset(ctx context.Context, repoID, path string) (MavenAsset, error) {
	var a MavenAsset
	err := s.db.QueryRowContext(ctx, `SELECT a.repository_id::text,a.path,a.object_key,a.digest,a.size FROM native_maven_assets a JOIN native_maven_artifacts m ON m.repository_id=a.repository_id AND m.state='visible' AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/')) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/' WHERE a.repository_id::text=$1 AND a.path=$2`, repoID, path).Scan(&a.RepositoryID, &a.Path, &a.ObjectKey, &a.Digest, &a.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}
func (s *PostgresStore) ListMavenArtifacts(ctx context.Context, repoID string) ([]MavenArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at FROM native_maven_artifacts WHERE repository_id::text=$1 AND state='visible' ORDER BY created_at DESC`, repoID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MavenArtifact{}
	for rows.Next() {
		var a MavenArtifact
		if err := rows.Scan(&a.ID, &a.RepositoryID, &a.Coordinate, &a.Digest, &a.State, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *PostgresStore) SearchMavenArtifacts(ctx context.Context, repoID, prefix string, limit int, after string) ([]MavenArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at
		FROM native_maven_artifacts
		WHERE repository_id=$1::uuid AND state='visible' AND substring(coordinate FROM 1 FOR char_length($2))=$2 AND coordinate>$3
		ORDER BY coordinate ASC LIMIT $4`, repoID, prefix, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MavenArtifact{}
	for rows.Next() {
		var artifact MavenArtifact
		if err := rows.Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, rows.Err()
}
func (s *PostgresStore) GetMavenArtifact(ctx context.Context, repositoryID, artifactID string) (MavenArtifact, error) {
	var artifact MavenArtifact
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at FROM native_maven_artifacts WHERE repository_id::text=$1 AND id::text=$2`, repositoryID, artifactID).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNotFound
	}
	return artifact, err
}

func (s *PostgresStore) GetMavenArtifactByCoordinate(ctx context.Context, repositoryID, coordinate string) (MavenArtifact, error) {
	var artifact MavenArtifact
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at FROM native_maven_artifacts WHERE repository_id::text=$1 AND coordinate=$2`, repositoryID, coordinate).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNotFound
	}
	return artifact, err
}
func (s *PostgresStore) TombstoneMavenArtifact(ctx context.Context, repositoryID, artifactID string) (MavenArtifact, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenArtifact{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var artifact MavenArtifact
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at FROM native_maven_artifacts WHERE repository_id::text=$1 AND id::text=$2 FOR UPDATE`, repositoryID, artifactID).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNotFound
	}
	if err != nil {
		return MavenArtifact{}, err
	}
	if artifact.State == "deleted" {
		return artifact, tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_artifacts SET state='deleted' WHERE id::text=$1`, artifactID); err != nil {
		return MavenArtifact{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artifact_tombstones (repository_id,format,coordinate,digest) VALUES ($1,'maven',$2,$3) ON CONFLICT DO NOTHING`, repositoryID, artifact.Coordinate, artifact.Digest); err != nil {
		return MavenArtifact{}, err
	}
	prefix := mavenArtifactPathPrefix(artifact.Coordinate)
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_object_intents i SET created_at=now() WHERE i.object_key IN (SELECT a.object_key FROM native_maven_assets a WHERE a.repository_id::text=$1 AND left(a.path, length($2))=$2) AND NOT EXISTS (SELECT 1 FROM native_maven_assets a JOIN native_maven_artifacts m ON m.repository_id=a.repository_id AND m.state='visible' WHERE a.object_key=i.object_key AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/')) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/')`, repositoryID, prefix); err != nil {
		return MavenArtifact{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_maven_object_references r WHERE r.object_key IN (SELECT a.object_key FROM native_maven_assets a WHERE a.repository_id::text=$1 AND left(a.path, length($2))=$2) AND NOT EXISTS (SELECT 1 FROM native_maven_assets a JOIN native_maven_artifacts m ON m.repository_id=a.repository_id AND m.state='visible' WHERE a.object_key=r.object_key AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/')) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/')`, repositoryID, prefix); err != nil {
		return MavenArtifact{}, err
	}
	artifact.State = "deleted"
	if err = tx.Commit(); err != nil {
		return MavenArtifact{}, err
	}
	return artifact, nil
}

func (s *PostgresStore) RestoreMavenArtifact(ctx context.Context, repositoryID, artifactID string) (MavenArtifact, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenArtifact{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var artifact MavenArtifact
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at FROM native_maven_artifacts WHERE repository_id::text=$1 AND id::text=$2 FOR UPDATE`, repositoryID, artifactID).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNotFound
	}
	if err != nil || artifact.State != "deleted" {
		if err != nil {
			return MavenArtifact{}, err
		}
		return MavenArtifact{}, ErrNotFound
	}
	prefix := mavenArtifactPathPrefix(artifact.Coordinate)
	var recoverable bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_maven_assets a WHERE a.repository_id::text=$1 AND left(a.path, length($2))=$2) AND NOT EXISTS (SELECT 1 FROM native_maven_assets a JOIN native_maven_object_intents i ON i.object_key=a.object_key WHERE a.repository_id::text=$1 AND left(a.path, length($2))=$2 AND i.deleted_at IS NOT NULL)`, repositoryID, prefix).Scan(&recoverable)
	if err != nil {
		return MavenArtifact{}, err
	}
	if !recoverable {
		return MavenArtifact{}, ErrDisabled
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_artifacts SET state='visible' WHERE id::text=$1`, artifactID); err != nil {
		return MavenArtifact{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM artifact_tombstones WHERE repository_id::text=$1 AND format='maven' AND coordinate=$2`, repositoryID, artifact.Coordinate); err != nil {
		return MavenArtifact{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) SELECT object_key,repository_id FROM native_maven_assets WHERE repository_id::text=$1 AND left(path, length($2))=$2 ON CONFLICT (object_key) DO NOTHING`, repositoryID, prefix); err != nil {
		return MavenArtifact{}, err
	}
	artifact.State = "visible"
	if err = tx.Commit(); err != nil {
		return MavenArtifact{}, err
	}
	return artifact, nil
}
func (s *PostgresStore) ClaimExpiredMavenObjectIntents(ctx context.Context, before time.Time, limit int) ([]MavenObjectIntent, error) {
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (SELECT i.object_key FROM native_maven_object_intents i JOIN native_maven_publish_sessions s ON s.id=i.session_id WHERE i.created_at <= $1 AND (i.claimed_at IS NULL OR i.claimed_at <= now() - interval '5 minutes') AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key) AND NOT (s.state='open' AND s.expires_at > now()) ORDER BY i.created_at FOR UPDATE OF s, i SKIP LOCKED LIMIT $2) UPDATE native_maven_object_intents i SET claimed_at=now(), claimed_token=md5(random()::text || clock_timestamp()::text || i.object_key) FROM candidates JOIN native_maven_publish_sessions s ON s.id=i.session_id WHERE i.object_key=candidates.object_key RETURNING s.repository_id::text, i.object_key, i.claimed_token`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MavenObjectIntent{}
	for rows.Next() {
		var v MavenObjectIntent
		if err := rows.Scan(&v.RepositoryID, &v.ObjectKey, &v.ClaimToken); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *PostgresStore) MavenObjectIntentClaimIsActive(ctx context.Context, key, claimToken string) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_maven_object_intents i WHERE i.object_key=$1 AND i.claim_token=$2 AND i.claimed_at IS NOT NULL AND i.claimed_at > now() - interval '5 minutes' AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key))`, key, claimToken).Scan(&active)
	return active, err
}
func (s *PostgresStore) MavenObjectIntentHasReference(ctx context.Context, key string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_maven_object_references WHERE object_key=$1)`, key).Scan(&referenced)
	return referenced, err
}
func (s *PostgresStore) ReleaseClaimedMavenObjectIntent(ctx context.Context, key, claimToken string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_maven_object_intents i SET claimed_at=NULL,claimed_token=NULL WHERE i.object_key=$1 AND i.claimed_token=$2 AND i.claimed_at IS NOT NULL AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key)`, key, claimToken)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) DeleteClaimedMavenObjectIntent(ctx context.Context, key, claimToken string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_maven_object_intents i SET deleted_at=now() WHERE i.object_key=$1 AND i.claimed_token=$2 AND i.claimed_at IS NOT NULL AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key)`, key, claimToken)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
