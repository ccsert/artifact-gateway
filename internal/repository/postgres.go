package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(databaseURL string) (*PostgresStore, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) Close() error { return s.db.Close() }

func (s *PostgresStore) CreateHostedRepository(ctx context.Context, repo HostedRepository) (HostedRepository, error) {
	err := s.db.QueryRowContext(ctx, `INSERT INTO hosted_repositories (id, name, format, state, version) VALUES ($1,$2,$3,'active',1) RETURNING state, version, created_at`, repo.ID, repo.Name, repo.Format).Scan(&repo.State, &repo.Version, &repo.CreatedAt)
	if isUnique(err) {
		return HostedRepository{}, ErrNameExists
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return repo, nil
}

func (s *PostgresStore) CreateHostedRepositoryIdempotently(ctx context.Context, repo HostedRepository, actor, key, payload string) (HostedRepository, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HostedRepository{}, false, err
	}
	defer tx.Rollback()
	// Row locks cannot serialize the first use of a key because no row exists
	// yet. A transaction-scoped advisory lock covers that gap without holding a
	// process-local mutex across gateway instances.
	// PostgreSQL text parameters reject NUL bytes. Prefix the variable parts
	// with their lengths so distinct actor/key pairs cannot share a lock key.
	lockKey := fmt.Sprintf("%d:%s%d:%s", len(actor), actor, len(key), key)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return HostedRepository{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM hosted_repository_idempotency WHERE actor=$1 AND target=$2 AND key=$3 AND expires_at <= now()`, actor, "/repositories", key); err != nil {
		return HostedRepository{}, false, err
	}
	var storedPayload, repositoryID string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash, repository_id::text FROM hosted_repository_idempotency WHERE actor=$1 AND target=$2 AND key=$3 AND expires_at > now() FOR UPDATE`, actor, "/repositories", key).Scan(&storedPayload, &repositoryID)
	if err == nil {
		if storedPayload != payload {
			return HostedRepository{}, false, ErrIdempotencyConflict
		}
		err = tx.QueryRowContext(ctx, `SELECT id::text, name, format, state, version::text, created_at FROM hosted_repositories WHERE id::text=$1`, repositoryID).Scan(&repo.ID, &repo.Name, &repo.Format, &repo.State, &repo.Version, &repo.CreatedAt)
		if err != nil {
			return HostedRepository{}, false, err
		}
		if err = tx.Commit(); err != nil {
			return HostedRepository{}, false, err
		}
		return repo, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return HostedRepository{}, false, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO hosted_repositories (id, name, format, state, version) VALUES ($1,$2,$3,'active',1) RETURNING state, version, created_at`, repo.ID, repo.Name, repo.Format).Scan(&repo.State, &repo.Version, &repo.CreatedAt)
	if isUnique(err) {
		return HostedRepository{}, false, ErrNameExists
	}
	if err != nil {
		return HostedRepository{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO hosted_repository_idempotency (actor, target, key, payload_hash, repository_id, expires_at) VALUES ($1,$2,$3,$4,$5,now() + interval '24 hours')`, actor, "/repositories", key, payload, repo.ID)
	if err != nil {
		return HostedRepository{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return HostedRepository{}, false, err
	}
	return repo, false, nil
}

func (s *PostgresStore) ListHostedRepositories(ctx context.Context, limit int, after string) ([]HostedRepository, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if after != "" {
		var exists bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, after).Scan(&exists); err != nil {
			return nil, "", err
		}
		if !exists {
			return nil, "", ErrNotFound
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, name, format, state, version::text, created_at FROM hosted_repositories WHERE ($1 = '' OR id::text > $1) ORDER BY id LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]HostedRepository, 0, limit)
	for rows.Next() {
		var repo HostedRepository
		if err := rows.Scan(&repo.ID, &repo.Name, &repo.Format, &repo.State, &repo.Version, &repo.CreatedAt); err != nil {
			return nil, "", err
		}
		items = append(items, repo)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	return items, next, nil
}

func (s *PostgresStore) GetHostedRepositoryByName(ctx context.Context, name string) (HostedRepository, error) {
	var repo HostedRepository
	err := s.db.QueryRowContext(ctx, `SELECT id::text, name, format, state, version::text, created_at FROM hosted_repositories WHERE name=$1`, name).Scan(&repo.ID, &repo.Name, &repo.Format, &repo.State, &repo.Version, &repo.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HostedRepository{}, ErrNotFound
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return repo, nil
}

func (s *PostgresStore) GetHostedRepository(ctx context.Context, id string) (HostedRepository, error) {
	var repo HostedRepository
	err := s.db.QueryRowContext(ctx, `SELECT id::text, name, format, state, version::text, created_at FROM hosted_repositories WHERE id::text=$1`, id).Scan(&repo.ID, &repo.Name, &repo.Format, &repo.State, &repo.Version, &repo.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HostedRepository{}, ErrNotFound
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return repo, nil
}

func (s *PostgresStore) DisableHostedRepository(ctx context.Context, id string) (HostedRepository, error) {
	var repo HostedRepository
	err := s.db.QueryRowContext(ctx, `UPDATE hosted_repositories SET state='deleting', version=version+1 WHERE id::text=$1 AND state='active' RETURNING id::text, name, format, state, version::text, created_at`, id).Scan(&repo.ID, &repo.Name, &repo.Format, &repo.State, &repo.Version, &repo.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return HostedRepository{}, ErrNotFound
	}
	if err != nil {
		return HostedRepository{}, err
	}
	return repo, nil
}

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
	defer tx.Rollback()
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
	defer tx.Rollback()
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
		if _, err = tx.ExecContext(ctx, `UPDATE native_maven_object_intents SET session_id=$2,digest=$3,size=$4,created_at=now(),claimed_at=NULL,deleted_at=NULL WHERE object_key=$1`, key, id, declared.Digest, declared.Size); err != nil {
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MavenArtifact{}, err
	}
	defer tx.Rollback()
	var v MavenPublishSession
	var objects []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,publisher,pom_object,state,expires_at,objects FROM native_maven_publish_sessions WHERE id::text=$1 FOR UPDATE`, id).Scan(&v.ID, &v.RepositoryID, &v.Coordinate, &v.Publisher, &v.PomObject, &v.State, &v.ExpiresAt, &objects)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNotFound
	}
	if err != nil {
		return MavenArtifact{}, err
	}
	if v.State != "open" || time.Now().After(v.ExpiresAt) {
		return MavenArtifact{}, ErrDisabled
	}
	if err = json.Unmarshal(objects, &v.Objects); err != nil {
		return MavenArtifact{}, err
	}
	var uploaded int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM native_maven_publish_uploads WHERE session_id=$1`, id).Scan(&uploaded); err != nil {
		return MavenArtifact{}, err
	}
	if uploaded != len(v.Objects) {
		return MavenArtifact{}, ErrDisabled
	}
	var claimed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_maven_object_intents WHERE session_id=$1 AND claimed_at IS NOT NULL)`, id).Scan(&claimed); err != nil {
		return MavenArtifact{}, err
	}
	if claimed {
		return MavenArtifact{}, ErrDisabled
	}
	for _, a := range assets {
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_intents (object_key,session_id,digest,size) VALUES ($1,$2,$3,$4) ON CONFLICT (object_key) DO NOTHING`, a.ObjectKey, id, a.Digest, a.Size); err != nil {
			return MavenArtifact{}, err
		}
		var intentClaimed, intentDeleted bool
		if err = tx.QueryRowContext(ctx, `SELECT claimed_at IS NOT NULL, deleted_at IS NOT NULL FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, a.ObjectKey).Scan(&intentClaimed, &intentDeleted); err != nil {
			return MavenArtifact{}, err
		}
		if intentClaimed || intentDeleted {
			return MavenArtifact{}, ErrDisabled
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO native_maven_assets (repository_id,path,object_key,digest,size) VALUES ($1,$2,$3,$4,$5)`, a.RepositoryID, a.Path, a.ObjectKey, a.Digest, a.Size)
		if isUnique(err) {
			return MavenArtifact{}, ErrNameExists
		}
		if err != nil {
			return MavenArtifact{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO native_maven_object_references (object_key,repository_id) VALUES ($1,$2) ON CONFLICT (object_key) DO NOTHING`, a.ObjectKey, a.RepositoryID); err != nil {
			return MavenArtifact{}, err
		}
	}
	a := MavenArtifact{ID: id, RepositoryID: v.RepositoryID, Coordinate: v.Coordinate, Digest: v.Objects[0].Digest, State: "visible", CreatedAt: time.Now().UTC()}
	err = tx.QueryRowContext(ctx, `INSERT INTO native_maven_artifacts (id,repository_id,coordinate,digest,state) VALUES ($1,$2,$3,$4,'visible') ON CONFLICT (repository_id,coordinate) DO NOTHING RETURNING id::text, digest, state, created_at`, a.ID, a.RepositoryID, a.Coordinate, a.Digest).Scan(&a.ID, &a.Digest, &a.State, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return MavenArtifact{}, ErrNameExists
	}
	if err != nil {
		return MavenArtifact{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_publish_sessions SET state='committed' WHERE id=$1`, id); err != nil {
		return MavenArtifact{}, err
	}
	if err = tx.Commit(); err != nil {
		return MavenArtifact{}, err
	}
	return a, nil
}
func (s *PostgresStore) GetMavenAsset(ctx context.Context, repoID, path string) (MavenAsset, error) {
	var a MavenAsset
	err := s.db.QueryRowContext(ctx, `SELECT repository_id::text,path,object_key,digest,size FROM native_maven_assets WHERE repository_id::text=$1 AND path=$2`, repoID, path).Scan(&a.RepositoryID, &a.Path, &a.ObjectKey, &a.Digest, &a.Size)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}
func (s *PostgresStore) ListMavenArtifacts(ctx context.Context, repoID string) ([]MavenArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at FROM native_maven_artifacts WHERE repository_id::text=$1 ORDER BY created_at DESC`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
func (s *PostgresStore) ClaimExpiredMavenObjectIntents(ctx context.Context, before time.Time, limit int) ([]MavenObjectIntent, error) {
	rows, err := s.db.QueryContext(ctx, `WITH claimed AS (SELECT i.object_key FROM native_maven_object_intents i JOIN native_maven_publish_sessions s ON s.id=i.session_id WHERE i.created_at <= $1 AND i.claimed_at IS NULL AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key) AND NOT (s.state='open' AND s.expires_at > now()) ORDER BY i.created_at FOR UPDATE OF s, i SKIP LOCKED LIMIT $2) UPDATE native_maven_object_intents i SET claimed_at=now() FROM claimed WHERE i.object_key=claimed.object_key RETURNING i.object_key`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MavenObjectIntent{}
	for rows.Next() {
		var v MavenObjectIntent
		if err := rows.Scan(&v.ObjectKey); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *PostgresStore) MavenObjectIntentHasReference(ctx context.Context, key string) (bool, error) {
	var referenced bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM native_maven_object_references WHERE object_key=$1)`, key).Scan(&referenced)
	return referenced, err
}
func (s *PostgresStore) ReleaseClaimedMavenObjectIntent(ctx context.Context, key string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_maven_object_intents i SET claimed_at=NULL WHERE i.object_key=$1 AND i.claimed_at IS NOT NULL AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key)`, key)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) DeleteClaimedMavenObjectIntent(ctx context.Context, key string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE native_maven_object_intents i SET deleted_at=now() WHERE i.object_key=$1 AND i.claimed_at IS NOT NULL AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key)`, key)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CreateGroup(ctx context.Context, group Group) (Group, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback() }()
	normalizeGroup(&group)
	err = tx.QueryRowContext(ctx, `INSERT INTO oci_groups (name, enabled, anonymous) VALUES ($1, true, $2) RETURNING created_at`, group.Name, group.Anonymous).Scan(&group.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameExists
		}
		return Group{}, err
	}
	for _, member := range group.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO oci_group_members (group_name, name, member_type, endpoint, position, anonymous, allowed_hosts) VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7::text[], '{}'::text[]))`, group.Name, member.Name, member.Type, member.Endpoint, member.Position, member.Anonymous, member.AllowedHosts); err != nil {
			return Group{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}

func (s *PostgresStore) GetGroup(ctx context.Context, name string) (Group, error) {
	var group Group
	if err := s.db.QueryRowContext(ctx, `SELECT name, enabled, anonymous, created_at FROM oci_groups WHERE name=$1`, name).Scan(&group.Name, &group.Enabled, &group.Anonymous, &group.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, member_type, endpoint, position, anonymous, array_to_json(allowed_hosts) FROM oci_group_members WHERE group_name=$1 ORDER BY position`, name)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var member Member
		var allowedHosts []byte
		if err := rows.Scan(&member.Name, &member.Type, &member.Endpoint, &member.Position, &member.Anonymous, &allowedHosts); err != nil {
			return Group{}, err
		}
		if err := json.Unmarshal(allowedHosts, &member.AllowedHosts); err != nil {
			return Group{}, err
		}
		group.Members = append(group.Members, member)
	}
	return group, rows.Err()
}

func (s *PostgresStore) DisableGroup(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE oci_groups SET enabled=false WHERE name=$1`, name)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CreateRawGroup(ctx context.Context, group Group) (Group, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback() }()
	normalizeGroup(&group)
	err = tx.QueryRowContext(ctx, `INSERT INTO raw_groups (name,enabled,anonymous,cache_quota_bytes) VALUES ($1,true,$2,$3) RETURNING created_at`, group.Name, group.Anonymous, group.CacheQuotaBytes).Scan(&group.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameExists
		}
		return Group{}, err
	}
	for _, m := range group.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO raw_group_members (group_name,name,member_type,endpoint,position,anonymous,allowed_hosts) VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7::text[], '{}'::text[]))`, group.Name, m.Name, m.Type, m.Endpoint, m.Position, m.Anonymous, m.AllowedHosts); err != nil {
			return Group{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}
func (s *PostgresStore) GetRawGroup(ctx context.Context, name string) (Group, error) {
	var g Group
	if err := s.db.QueryRowContext(ctx, `SELECT name,enabled,anonymous,cache_quota_bytes,created_at FROM raw_groups WHERE name=$1`, name).Scan(&g.Name, &g.Enabled, &g.Anonymous, &g.CacheQuotaBytes, &g.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name,member_type,endpoint,position,anonymous,array_to_json(allowed_hosts) FROM raw_group_members WHERE group_name=$1 ORDER BY position`, name)
	if err != nil {
		return Group{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Member
		var allowedHosts []byte
		if err := rows.Scan(&m.Name, &m.Type, &m.Endpoint, &m.Position, &m.Anonymous, &allowedHosts); err != nil {
			return Group{}, err
		}
		if err := json.Unmarshal(allowedHosts, &m.AllowedHosts); err != nil {
			return Group{}, err
		}
		g.Members = append(g.Members, m)
	}
	return g, rows.Err()
}
func (s *PostgresStore) DisableRawGroup(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE raw_groups SET enabled=false WHERE name=$1`, name)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) RecordAudit(ctx context.Context, audit AuditRecord) error {
	if audit.OccurredAt.IsZero() {
		audit.OccurredAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO resolver_audit_log (group_name, repository, member_name, outcome, actor, occurred_at, format, resource, representation, member_type, upstream_host, operation, http_status, cache_disposition, bytes, request_id, trace_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`, audit.GroupName, audit.Repository, audit.MemberName, audit.Outcome, audit.Actor, audit.OccurredAt, audit.Format, audit.Resource, audit.Representation, audit.MemberType, audit.UpstreamHost, audit.Operation, audit.Status, audit.CacheDisposition, audit.Bytes, audit.RequestID, audit.TraceID)
	return err
}

func (s *PostgresStore) ListAudits(ctx context.Context, query AuditQuery) ([]AuditRecord, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT group_name, repository, member_name, outcome, actor, occurred_at,
		COALESCE(format, ''), COALESCE(resource, ''), COALESCE(representation, ''), COALESCE(member_type, ''), COALESCE(upstream_host, ''), COALESCE(operation, ''),
		COALESCE(http_status, 0), COALESCE(cache_disposition, ''), COALESCE(bytes, 0), COALESCE(request_id, ''), COALESCE(trace_id, '')
		FROM resolver_audit_log
		WHERE ($1 = '' OR group_name = $1) AND ($2 = '' OR repository = $2)
		ORDER BY occurred_at DESC, id DESC
		LIMIT $3`, query.GroupName, query.Repository, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var audits []AuditRecord
	for rows.Next() {
		var audit AuditRecord
		if err := rows.Scan(&audit.GroupName, &audit.Repository, &audit.MemberName, &audit.Outcome, &audit.Actor, &audit.OccurredAt, &audit.Format, &audit.Resource, &audit.Representation, &audit.MemberType, &audit.UpstreamHost, &audit.Operation, &audit.Status, &audit.CacheDisposition, &audit.Bytes, &audit.RequestID, &audit.TraceID); err != nil {
			return nil, err
		}
		audits = append(audits, audit)
	}
	return audits, rows.Err()
}

func (s *PostgresStore) CreateMavenGroup(ctx context.Context, group Group) (Group, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback() }()
	normalizeGroup(&group)
	err = tx.QueryRowContext(ctx, `INSERT INTO maven_groups (name, enabled, anonymous) VALUES ($1, true, $2) RETURNING created_at`, group.Name, group.Anonymous).Scan(&group.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameExists
		}
		return Group{}, err
	}
	for _, member := range group.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO maven_group_members (group_name, name, member_type, endpoint, position, anonymous) VALUES ($1,$2,$3,$4,$5,$6)`, group.Name, member.Name, member.Type, member.Endpoint, member.Position, member.Anonymous); err != nil {
			return Group{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}

func (s *PostgresStore) GetMavenGroup(ctx context.Context, name string) (Group, error) {
	var group Group
	if err := s.db.QueryRowContext(ctx, `SELECT name, enabled, anonymous, created_at FROM maven_groups WHERE name=$1`, name).Scan(&group.Name, &group.Enabled, &group.Anonymous, &group.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, member_type, endpoint, position, anonymous FROM maven_group_members WHERE group_name=$1 ORDER BY position`, name)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.Name, &member.Type, &member.Endpoint, &member.Position, &member.Anonymous); err != nil {
			return Group{}, err
		}
		group.Members = append(group.Members, member)
	}
	return group, rows.Err()
}

func (s *PostgresStore) DisableMavenGroup(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE maven_groups SET enabled=false WHERE name=$1`, name)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CreateConanGroup(ctx context.Context, group Group) (Group, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if group.CacheQuotaBytes == 0 {
		group.CacheQuotaBytes = 1 << 30
	}
	normalizeGroup(&group)
	err = tx.QueryRowContext(ctx, `INSERT INTO conan_groups (name, enabled, anonymous, cache_quota_bytes) VALUES ($1, true, $2, $3) RETURNING created_at`, group.Name, group.Anonymous, group.CacheQuotaBytes).Scan(&group.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameExists
		}
		return Group{}, err
	}
	for _, member := range group.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO conan_group_members (group_name, name, member_type, endpoint, position, anonymous, allowed_hosts) VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7::text[], '{}'::text[]))`, group.Name, member.Name, member.Type, member.Endpoint, member.Position, member.Anonymous, member.AllowedHosts); err != nil {
			return Group{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}
func (s *PostgresStore) GetConanGroup(ctx context.Context, name string) (Group, error) {
	var group Group
	if err := s.db.QueryRowContext(ctx, `SELECT name, enabled, anonymous, cache_quota_bytes, created_at FROM conan_groups WHERE name=$1`, name).Scan(&group.Name, &group.Enabled, &group.Anonymous, &group.CacheQuotaBytes, &group.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, member_type, endpoint, position, anonymous, array_to_json(allowed_hosts) FROM conan_group_members WHERE group_name=$1 ORDER BY position`, name)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var member Member
		var allowedHosts []byte
		if err := rows.Scan(&member.Name, &member.Type, &member.Endpoint, &member.Position, &member.Anonymous, &allowedHosts); err != nil {
			return Group{}, err
		}
		if err := json.Unmarshal(allowedHosts, &member.AllowedHosts); err != nil {
			return Group{}, err
		}
		group.Members = append(group.Members, member)
	}
	return group, rows.Err()
}
func (s *PostgresStore) DisableConanGroup(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE conan_groups SET enabled=false WHERE name=$1`, name)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func isUnique(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
