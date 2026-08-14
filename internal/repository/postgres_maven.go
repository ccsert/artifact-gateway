package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

func (s *PostgresStore) CreateMavenPublishSession(ctx context.Context, v MavenPublishSession) (MavenPublishSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenPublishSession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET state='expired' WHERE repository_id::text=$1 AND coordinate=$2 AND publisher=$3 AND state='open' AND expires_at<=now()`, v.RepositoryID, v.Coordinate, v.Publisher); err != nil {
		return MavenPublishSession{}, err
	}
	objects, _ := json.Marshal(v.Objects)
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_publish_sessions (id,repository_id,coordinate,publisher,pom_object,state,expires_at,objects) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, v.ID, v.RepositoryID, v.Coordinate, v.Publisher, v.PomObject, v.State, v.ExpiresAt, objects); err != nil {
		if isUnique(err) {
			return MavenPublishSession{}, ErrNameExists
		}
		return MavenPublishSession{}, err
	}
	if err = tx.Commit(); err != nil {
		return MavenPublishSession{}, err
	}
	return v, nil
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
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,publisher,pom_object,state,expires_at,objects FROM native_maven_publish_sessions WHERE repository_id::text=$1 AND coordinate=$2 AND publisher=$3 AND state='open' AND expires_at>now()`, repoID, coordinate, publisher).Scan(&v.ID, &v.RepositoryID, &v.Coordinate, &v.Publisher, &v.PomObject, &v.State, &v.ExpiresAt, &raw)
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
			if err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number FROM native_maven_artifacts WHERE id=$1`, id).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt, &artifact.BuildNumber); err != nil {
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
	a := MavenArtifact{ID: id, RepositoryID: v.RepositoryID, Coordinate: v.Coordinate, Digest: v.Objects[0].Digest, State: "visible", CreatedAt: time.Now().UTC()}
	if IsMavenSnapshotCoordinate(v.Coordinate) {
		// SNAPSHOT coordinates accumulate one row per build. The build number is
		// allocated inside the commit transaction; a concurrent commit of the
		// same coordinate loses the unique-index race and is rejected, and its
		// caller may retry the whole commit to take the next number.
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(build_number),0)+1 FROM native_maven_artifacts WHERE repository_id=$1 AND coordinate=$2`, a.RepositoryID, a.Coordinate).Scan(&a.BuildNumber); err != nil {
			return MavenArtifact{}, false, err
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state,build_number) VALUES ($1,$2,$3,$4,'visible',$5) RETURNING id::text, digest, state, created_at`, a.ID, a.RepositoryID, a.Coordinate, a.Digest, a.BuildNumber).Scan(&a.ID, &a.Digest, &a.State, &a.CreatedAt)
		if isUnique(err) {
			return MavenArtifact{}, false, ErrNameExists
		}
		if err != nil {
			return MavenArtifact{}, false, err
		}
		// Store the build's assets under Maven's timestamped filenames so
		// repeated publishes of the same -SNAPSHOT coordinate never collide.
		// The filename timestamp and build number come from the same row.
		for _, asset := range assets {
			asset.Path = mavenSnapshotTimestampedPath(asset.Path, v.Coordinate, a.CreatedAt, a.BuildNumber)
			if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_intents (object_key,session_id,digest,size) VALUES ($1,$2,$3,$4) ON CONFLICT (object_key) DO NOTHING`, asset.ObjectKey, id, asset.Digest, asset.Size); err != nil {
				return MavenArtifact{}, false, err
			}
			var intentClaimed, intentDeleted bool
			if err = tx.QueryRowContext(ctx, `SELECT claimed_at IS NOT NULL, deleted_at IS NOT NULL FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, asset.ObjectKey).Scan(&intentClaimed, &intentDeleted); err != nil {
				return MavenArtifact{}, false, err
			}
			if intentClaimed || intentDeleted {
				return MavenArtifact{}, false, ErrDisabled
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO native_maven_assets (repository_id,path,object_key,digest,size) VALUES ($1,$2,$3,$4,$5)`, asset.RepositoryID, asset.Path, asset.ObjectKey, asset.Digest, asset.Size)
			if isUnique(err) {
				return MavenArtifact{}, false, ErrNameExists
			}
			if err != nil {
				return MavenArtifact{}, false, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) VALUES ($1,$2) ON CONFLICT (object_key) DO NOTHING`, asset.ObjectKey, asset.RepositoryID); err != nil {
				return MavenArtifact{}, false, err
			}
		}
	} else {
		for _, asset := range assets {
			if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_intents (object_key,session_id,digest,size) VALUES ($1,$2,$3,$4) ON CONFLICT (object_key) DO NOTHING`, asset.ObjectKey, id, asset.Digest, asset.Size); err != nil {
				return MavenArtifact{}, false, err
			}
			var intentClaimed, intentDeleted bool
			if err = tx.QueryRowContext(ctx, `SELECT claimed_at IS NOT NULL, deleted_at IS NOT NULL FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, asset.ObjectKey).Scan(&intentClaimed, &intentDeleted); err != nil {
				return MavenArtifact{}, false, err
			}
			if intentClaimed || intentDeleted {
				return MavenArtifact{}, false, ErrDisabled
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO native_maven_assets (repository_id,path,object_key,digest,size) VALUES ($1,$2,$3,$4,$5)`, asset.RepositoryID, asset.Path, asset.ObjectKey, asset.Digest, asset.Size)
			if isUnique(err) {
				return MavenArtifact{}, false, ErrNameExists
			}
			if err != nil {
				return MavenArtifact{}, false, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) VALUES ($1,$2) ON CONFLICT (object_key) DO NOTHING`, asset.ObjectKey, asset.RepositoryID); err != nil {
				return MavenArtifact{}, false, err
			}
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state) VALUES ($1,$2,$3,$4,'visible') ON CONFLICT (repository_id,coordinate,build_number) DO NOTHING RETURNING id::text, digest, state, created_at`, a.ID, a.RepositoryID, a.Coordinate, a.Digest).Scan(&a.ID, &a.Digest, &a.State, &a.CreatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return MavenArtifact{}, false, ErrNameExists
		}
		if err != nil {
			return MavenArtifact{}, false, err
		}
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

// mavenSnapshotBuildFilePrefix is the filename prefix every asset of one
// SNAPSHOT build carries: "<artifact>-<baseVersion>-<timestamp>-<buildNumber>".
// Tombstone and restore scope their asset cleanup to it so sibling builds of
// the same -SNAPSHOT coordinate stay untouched.
func mavenSnapshotBuildFilePrefix(coordinate string, createdAt time.Time, buildNumber int) string {
	parts := strings.Split(coordinate, ":")
	base := strings.TrimSuffix(parts[2], "-SNAPSHOT")
	return parts[1] + "-" + base + "-" + createdAt.UTC().Format("20060102.150405") + "-" + strconv.Itoa(buildNumber)
}

// mavenSnapshotTimestampedPath rewrites a canonical SNAPSHOT asset path
// ("g/a/1.0-SNAPSHOT/a-1.0-SNAPSHOT.jar") into the build's timestamped path
// ("g/a/1.0-SNAPSHOT/a-1.0-20260730.084839-1.jar"). Names that do not start
// with the canonical "<artifact>-<version>" prefix are left unchanged.
func mavenSnapshotTimestampedPath(path, coordinate string, createdAt time.Time, buildNumber int) string {
	parts := strings.Split(coordinate, ":")
	canonical := parts[1] + "-" + parts[2]
	slash := strings.LastIndexByte(path, '/')
	dir, name := "", path
	if slash >= 0 {
		dir, name = path[:slash+1], path[slash+1:]
	}
	if !strings.HasPrefix(name, canonical) {
		return path
	}
	return dir + mavenSnapshotBuildFilePrefix(coordinate, createdAt, buildNumber) + strings.TrimPrefix(name, canonical)
}

func (s *PostgresStore) GetMavenAsset(ctx context.Context, repoID, path string) (MavenAsset, error) {
	var a MavenAsset
	// The join matches an asset to a visible artifact only when the path sits
	// under the artifact's own files: the version directory for releases, the
	// build's timestamped filename prefix for SNAPSHOT builds.
	err := s.db.QueryRowContext(ctx, `SELECT a.repository_id::text,a.path,a.object_key,a.digest,a.size FROM native_maven_assets a JOIN native_maven_artifacts m ON m.repository_id=a.repository_id AND m.state='visible' AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/' || CASE WHEN m.build_number > 0 THEN split_part(m.coordinate, ':', 2) || '-' || regexp_replace(split_part(m.coordinate, ':', 3), '-SNAPSHOT$', '') || '-' || to_char(m.created_at AT TIME ZONE 'UTC', 'YYYYMMDD.HH24MISS') || '-' || m.build_number ELSE '' END)) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/' || CASE WHEN m.build_number > 0 THEN split_part(m.coordinate, ':', 2) || '-' || regexp_replace(split_part(m.coordinate, ':', 3), '-SNAPSHOT$', '') || '-' || to_char(m.created_at AT TIME ZONE 'UTC', 'YYYYMMDD.HH24MISS') || '-' || m.build_number ELSE '' END WHERE a.repository_id::text=$1 AND a.path=$2`, repoID, path).Scan(&a.RepositoryID, &a.Path, &a.ObjectKey, &a.Digest, &a.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}
func (s *PostgresStore) ListMavenAssets(ctx context.Context, repoID, coordinate string) ([]MavenAsset, error) {
	prefix := mavenArtifactPathPrefix(coordinate)
	rows, err := s.db.QueryContext(ctx, `SELECT a.repository_id::text,a.path,a.object_key,a.digest,a.size FROM native_maven_assets a JOIN native_maven_artifacts m ON m.repository_id=a.repository_id AND m.coordinate=$2 AND m.state='visible' AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/' || CASE WHEN m.build_number > 0 THEN split_part(m.coordinate, ':', 2) || '-' || regexp_replace(split_part(m.coordinate, ':', 3), '-SNAPSHOT$', '') || '-' || to_char(m.created_at AT TIME ZONE 'UTC', 'YYYYMMDD.HH24MISS') || '-' || m.build_number ELSE '' END)) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/' || CASE WHEN m.build_number > 0 THEN split_part(m.coordinate, ':', 2) || '-' || regexp_replace(split_part(m.coordinate, ':', 3), '-SNAPSHOT$', '') || '-' || to_char(m.created_at AT TIME ZONE 'UTC', 'YYYYMMDD.HH24MISS') || '-' || m.build_number ELSE '' END WHERE a.repository_id::text=$1 AND left(a.path,length($3))=$3 ORDER BY a.path`, repoID, coordinate, prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	assets := []MavenAsset{}
	for rows.Next() {
		var asset MavenAsset
		if err := rows.Scan(&asset.RepositoryID, &asset.Path, &asset.ObjectKey, &asset.Digest, &asset.Size); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return nil, ErrNotFound
	}
	return assets, nil
}
func (s *PostgresStore) ListMavenArtifacts(ctx context.Context, repoID string) ([]MavenArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number FROM native_maven_artifacts WHERE repository_id::text=$1 AND state='visible' ORDER BY created_at DESC`, repoID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MavenArtifact{}
	for rows.Next() {
		var a MavenArtifact
		if err := rows.Scan(&a.ID, &a.RepositoryID, &a.Coordinate, &a.Digest, &a.State, &a.CreatedAt, &a.BuildNumber); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SearchMavenArtifacts pages visible artifacts in (coordinate, build_number)
// order so every SNAPSHOT build appears as its own row.
func (s *PostgresStore) SearchMavenArtifacts(ctx context.Context, repoID, prefix string, limit int, after MavenArtifactCursor) ([]MavenArtifact, error) {
	if limit <= 0 {
		limit = 100
	} else if limit > 2_147_483_647 {
		// PostgreSQL's LIMIT parameter is an int4; callers may use a large
		// logical limit to request an unbounded in-memory-style page.
		limit = 2_147_483_647
	}
	if after.BuildNumber > 2_147_483_647 {
		after.BuildNumber = 2_147_483_647
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id::text,a.repository_id::text,a.coordinate,a.digest,a.state,a.created_at,a.build_number,COALESCE(p.publisher,'')
		FROM native_maven_artifacts a
		LEFT JOIN LATERAL (
			SELECT s.publisher FROM native_maven_publish_sessions s
			WHERE s.repository_id=a.repository_id AND s.coordinate=a.coordinate AND s.state='committed'
			ORDER BY s.expires_at DESC LIMIT 1
		) p ON true
		WHERE a.repository_id=$1::uuid AND a.state='visible' AND a.coordinate LIKE $2 || '%' ESCAPE '\'
			AND (a.coordinate>$3 OR (a.coordinate=$3 AND a.build_number>$4))
		ORDER BY a.coordinate ASC, a.build_number ASC LIMIT $5`, repoID, escapeLikePrefix(prefix), after.Coordinate, after.BuildNumber, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []MavenArtifact{}
	for rows.Next() {
		var artifact MavenArtifact
		if err := rows.Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt, &artifact.BuildNumber, &artifact.Publisher); err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, rows.Err()
}
func (s *PostgresStore) GetMavenArtifact(ctx context.Context, repositoryID, artifactID string) (MavenArtifact, error) {
	var artifact MavenArtifact
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number FROM native_maven_artifacts WHERE repository_id::text=$1 AND id::text=$2`, repositoryID, artifactID).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt, &artifact.BuildNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNotFound
	}
	return artifact, err
}

// GetMavenArtifactByCoordinate returns the latest build of the coordinate:
// the only row for releases, the highest build number for SNAPSHOT
// coordinates. Tombstoned builds stay eligible so restore flows can find
// them by coordinate.
func (s *PostgresStore) GetMavenArtifactByCoordinate(ctx context.Context, repositoryID, coordinate string) (MavenArtifact, error) {
	var artifact MavenArtifact
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number FROM native_maven_artifacts WHERE repository_id::text=$1 AND coordinate=$2 ORDER BY build_number DESC LIMIT 1`, repositoryID, coordinate).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt, &artifact.BuildNumber)
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
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number FROM native_maven_artifacts WHERE repository_id::text=$1 AND id::text=$2 FOR UPDATE`, repositoryID, artifactID).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt, &artifact.BuildNumber)
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
	// SNAPSHOT builds share the version directory, so scope the cleanup to the
	// build's own timestamped filename prefix. Releases keep the whole
	// version-directory prefix because they own it exclusively.
	prefix := mavenArtifactPathPrefix(artifact.Coordinate)
	if artifact.BuildNumber > 0 {
		prefix += mavenSnapshotBuildFilePrefix(artifact.Coordinate, artifact.CreatedAt, artifact.BuildNumber)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_object_intents i SET created_at=now() WHERE i.object_key IN (SELECT a.object_key FROM native_maven_assets a WHERE a.repository_id::text=$1 AND left(a.path, length($2))=$2) AND NOT EXISTS (SELECT 1 FROM native_maven_assets a JOIN native_maven_artifacts m ON m.repository_id=a.repository_id AND m.state='visible' WHERE a.object_key=i.object_key AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/' || CASE WHEN m.build_number > 0 THEN split_part(m.coordinate, ':', 2) || '-' || regexp_replace(split_part(m.coordinate, ':', 3), '-SNAPSHOT$', '') || '-' || to_char(m.created_at AT TIME ZONE 'UTC', 'YYYYMMDD.HH24MISS') || '-' || m.build_number ELSE '' END)) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/' || CASE WHEN m.build_number > 0 THEN split_part(m.coordinate, ':', 2) || '-' || regexp_replace(split_part(m.coordinate, ':', 3), '-SNAPSHOT$', '') || '-' || to_char(m.created_at AT TIME ZONE 'UTC', 'YYYYMMDD.HH24MISS') || '-' || m.build_number ELSE '' END)`, repositoryID, prefix); err != nil {
		return MavenArtifact{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_maven_object_references r WHERE r.object_key IN (SELECT a.object_key FROM native_maven_assets a WHERE a.repository_id::text=$1 AND left(a.path, length($2))=$2) AND NOT EXISTS (SELECT 1 FROM native_maven_assets a JOIN native_maven_artifacts m ON m.repository_id=a.repository_id AND m.state='visible' WHERE a.object_key=r.object_key AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/' || CASE WHEN m.build_number > 0 THEN split_part(m.coordinate, ':', 2) || '-' || regexp_replace(split_part(m.coordinate, ':', 3), '-SNAPSHOT$', '') || '-' || to_char(m.created_at AT TIME ZONE 'UTC', 'YYYYMMDD.HH24MISS') || '-' || m.build_number ELSE '' END)) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/' || CASE WHEN m.build_number > 0 THEN split_part(m.coordinate, ':', 2) || '-' || regexp_replace(split_part(m.coordinate, ':', 3), '-SNAPSHOT$', '') || '-' || to_char(m.created_at AT TIME ZONE 'UTC', 'YYYYMMDD.HH24MISS') || '-' || m.build_number ELSE '' END)`, repositoryID, prefix); err != nil {
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
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number FROM native_maven_artifacts WHERE repository_id::text=$1 AND id::text=$2 FOR UPDATE`, repositoryID, artifactID).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt, &artifact.BuildNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNotFound
	}
	if err != nil || artifact.State != "deleted" {
		if err != nil {
			return MavenArtifact{}, err
		}
		return MavenArtifact{}, ErrNotFound
	}
	// Match the tombstone scoping: a SNAPSHOT build restores only its own
	// timestamped files, a release restores the whole version directory.
	prefix := mavenArtifactPathPrefix(artifact.Coordinate)
	if artifact.BuildNumber > 0 {
		prefix += mavenSnapshotBuildFilePrefix(artifact.Coordinate, artifact.CreatedAt, artifact.BuildNumber)
	}
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

func (s *PostgresStore) PromoteMavenArtifact(ctx context.Context, promotion MavenPromotion) (MavenArtifact, error) {
	// Cross-repository SNAPSHOT promotion is out of scope: snapshot builds are
	// repository-local and their timestamped asset names cannot be replayed
	// into another repository without a build-number allocation there.
	if IsMavenSnapshotCoordinate(promotion.Coordinate) {
		return MavenArtifact{}, ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenArtifact{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var source MavenArtifact
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number FROM native_maven_artifacts WHERE repository_id::text=$1 AND coordinate=$2 AND digest=$3 AND state='visible' FOR UPDATE`, promotion.SourceRepositoryID, promotion.Coordinate, promotion.Digest).Scan(&source.ID, &source.RepositoryID, &source.Coordinate, &source.Digest, &source.State, &source.CreatedAt, &source.BuildNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNotFound
	}
	if err != nil {
		return MavenArtifact{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state) VALUES ($1,$2,$3,$4,'visible') ON CONFLICT (repository_id,coordinate,build_number) DO NOTHING`, promotion.ID, promotion.TargetRepositoryID, source.Coordinate, source.Digest)
	if err != nil {
		return MavenArtifact{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return MavenArtifact{}, ErrNameExists
	}
	prefix := mavenArtifactPathPrefix(source.Coordinate)
	result, err = tx.ExecContext(ctx, `INSERT INTO native_maven_assets (repository_id,path,object_key,digest,size) SELECT $1,path,object_key,digest,size FROM native_maven_assets WHERE repository_id::text=$2 AND left(path,length($3))=$3`, promotion.TargetRepositoryID, promotion.SourceRepositoryID, prefix)
	if err != nil {
		return MavenArtifact{}, err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return MavenArtifact{}, ErrDisabled
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) SELECT object_key,$1::uuid FROM native_maven_assets WHERE repository_id=$1::uuid AND left(path,length($2))=$2 ON CONFLICT (object_key) DO NOTHING`, promotion.TargetRepositoryID, prefix); err != nil {
		return MavenArtifact{}, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT created_at FROM native_maven_artifacts WHERE id=$1`, promotion.ID).Scan(&source.CreatedAt); err != nil {
		return MavenArtifact{}, err
	}
	if err = tx.Commit(); err != nil {
		return MavenArtifact{}, err
	}
	return MavenArtifact{ID: promotion.ID, RepositoryID: promotion.TargetRepositoryID, Coordinate: source.Coordinate, Digest: source.Digest, State: "visible", CreatedAt: source.CreatedAt}, nil
}

func (s *PostgresStore) PublishReplicatedMavenArtifact(ctx context.Context, replication MavenReplication) (MavenArtifact, error) {
	if len(replication.Assets) == 0 {
		return MavenArtifact{}, ErrDisabled
	}
	// Cross-repository SNAPSHOT replication is out of scope for the same
	// reason as promotion.
	if IsMavenSnapshotCoordinate(replication.Coordinate) {
		return MavenArtifact{}, ErrDisabled
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenArtifact{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var source MavenArtifact
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number FROM native_maven_artifacts WHERE repository_id::text=$1 AND coordinate=$2 AND digest=$3 AND state='visible' FOR UPDATE`, replication.SourceRepositoryID, replication.Coordinate, replication.Digest).Scan(&source.ID, &source.RepositoryID, &source.Coordinate, &source.Digest, &source.State, &source.CreatedAt, &source.BuildNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNotFound
	}
	if err != nil {
		return MavenArtifact{}, err
	}
	var existing MavenArtifact
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at,build_number FROM native_maven_artifacts WHERE repository_id::text=$1 AND coordinate=$2 AND build_number=0 FOR UPDATE`, replication.TargetRepositoryID, replication.Coordinate).Scan(&existing.ID, &existing.RepositoryID, &existing.Coordinate, &existing.Digest, &existing.State, &existing.CreatedAt, &existing.BuildNumber)
	if err == nil {
		if existing.ID == replication.ID && existing.Digest == replication.Digest {
			return existing, tx.Commit()
		}
		return MavenArtifact{}, ErrNameExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, err
	}
	prefix := mavenArtifactPathPrefix(replication.Coordinate)
	for _, copied := range replication.Assets {
		var matched bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_maven_assets WHERE repository_id::text=$1 AND path=$2 AND object_key=$3 AND digest=$4 AND size=$5 AND left(path,length($6))=$6)`, replication.SourceRepositoryID, copied.Path, copied.SourceObjectKey, copied.Digest, copied.Size, prefix).Scan(&matched); err != nil {
			return MavenArtifact{}, err
		}
		if !matched {
			return MavenArtifact{}, ErrNotFound
		}
	}
	declared := make([]MavenDeclaredObject, 0, len(replication.Assets))
	for _, copied := range replication.Assets {
		declared = append(declared, MavenDeclaredObject{Name: strings.TrimPrefix(copied.Path, prefix), Digest: copied.Digest, Size: copied.Size})
	}
	objects, _ := json.Marshal(declared)
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_publish_sessions (id,repository_id,coordinate,publisher,pom_object,state,expires_at,objects) VALUES ($1,$2,$3,'replication',$4,'committed',now(),$5)`, replication.ID, replication.TargetRepositoryID, replication.Coordinate, declared[0].Name, objects); err != nil {
		return MavenArtifact{}, err
	}
	for _, copied := range replication.Assets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_intents (object_key,session_id,digest,size) VALUES ($1,$2,$3,$4) ON CONFLICT (object_key) DO NOTHING`, copied.ObjectKey, replication.ID, copied.Digest, copied.Size); err != nil {
			return MavenArtifact{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_assets (repository_id,path,object_key,digest,size) VALUES ($1,$2,$3,$4,$5)`, replication.TargetRepositoryID, copied.Path, copied.ObjectKey, copied.Digest, copied.Size); isUnique(err) {
			return MavenArtifact{}, ErrNameExists
		} else if err != nil {
			return MavenArtifact{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) VALUES ($1,$2) ON CONFLICT (object_key) DO NOTHING`, copied.ObjectKey, replication.TargetRepositoryID); err != nil {
			return MavenArtifact{}, err
		}
	}
	artifact := MavenArtifact{ID: replication.ID, RepositoryID: replication.TargetRepositoryID, Coordinate: replication.Coordinate, Digest: replication.Digest, State: "visible"}
	err = tx.QueryRowContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state) VALUES ($1,$2,$3,$4,'visible') RETURNING created_at`, artifact.ID, artifact.RepositoryID, artifact.Coordinate, artifact.Digest).Scan(&artifact.CreatedAt)
	if isUnique(err) {
		return MavenArtifact{}, ErrNameExists
	}
	if err != nil {
		return MavenArtifact{}, err
	}
	if err = tx.Commit(); err != nil {
		return MavenArtifact{}, err
	}
	return artifact, nil
}
func (s *PostgresStore) ClaimExpiredMavenObjectIntents(ctx context.Context, before time.Time, limit int) ([]MavenObjectIntent, error) {
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (SELECT i.object_key FROM native_maven_object_intents i JOIN native_maven_publish_sessions s ON s.id=i.session_id WHERE i.created_at <= $1 AND (i.claimed_at IS NULL OR i.claimed_at <= now() - interval '5 minutes') AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key) AND NOT (s.state='open' AND s.expires_at > now()) ORDER BY i.created_at FOR UPDATE OF s, i SKIP LOCKED LIMIT $2) UPDATE native_maven_object_intents i SET claimed_at=now(), claimed_token=md5(random()::text || clock_timestamp()::text || i.object_key) FROM candidates, native_maven_publish_sessions s WHERE i.object_key=candidates.object_key AND s.id=i.session_id RETURNING s.repository_id::text, i.object_key, i.claimed_token`, before, limit)
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
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_maven_object_intents i WHERE i.object_key=$1 AND i.claimed_token=$2 AND i.claimed_at IS NOT NULL AND i.claimed_at > now() - interval '5 minutes' AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key))`, key, claimToken).Scan(&active)
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
