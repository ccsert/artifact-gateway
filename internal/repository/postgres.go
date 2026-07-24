package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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

func scanHostedGroupMembers(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, group *HostedGroup) error {
	rows, err := query.QueryContext(ctx, `SELECT repository_id::text, position FROM hosted_group_members WHERE group_id::text=$1 ORDER BY position`, group.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	group.Members = nil
	for rows.Next() {
		var member GroupMember
		if err := rows.Scan(&member.RepositoryID, &member.Position); err != nil {
			return err
		}
		group.Members = append(group.Members, member)
	}
	return rows.Err()
}

func (s *PostgresStore) getHostedGroup(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, id string) (HostedGroup, error) {
	var group HostedGroup
	err := query.QueryRowContext(ctx, `SELECT id::text, name, format, version::text FROM hosted_groups WHERE id::text=$1`, id).Scan(&group.ID, &group.Name, &group.Format, &group.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return HostedGroup{}, ErrNotFound
	}
	if err != nil {
		return HostedGroup{}, err
	}
	if err := scanHostedGroupMembers(ctx, query, &group); err != nil {
		return HostedGroup{}, err
	}
	return group, nil
}

func (s *PostgresStore) CreateHostedGroupIdempotently(ctx context.Context, group HostedGroup, actor, key, payload string) (HostedGroup, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HostedGroup{}, false, err
	}
	defer tx.Rollback()
	lockKey := fmt.Sprintf("hosted-group:%d:%s%d:%s", len(actor), actor, len(key), key)
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return HostedGroup{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM hosted_group_idempotency WHERE actor=$1 AND key=$2 AND expires_at <= now()`, actor, key); err != nil {
		return HostedGroup{}, false, err
	}
	var storedPayload, groupID string
	err = tx.QueryRowContext(ctx, `SELECT payload_hash, group_id::text FROM hosted_group_idempotency WHERE actor=$1 AND key=$2 FOR UPDATE`, actor, key).Scan(&storedPayload, &groupID)
	if err == nil {
		if storedPayload != payload {
			return HostedGroup{}, false, ErrIdempotencyConflict
		}
		group, err = s.getHostedGroup(ctx, tx, groupID)
		if err != nil {
			return HostedGroup{}, false, err
		}
		if err = tx.Commit(); err != nil {
			return HostedGroup{}, false, err
		}
		return group, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return HostedGroup{}, false, err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO hosted_groups (id,name,format,version) VALUES ($1,$2,$3,1) RETURNING version::text`, group.ID, group.Name, group.Format).Scan(&group.Version); err != nil {
		if isUnique(err) {
			return HostedGroup{}, false, ErrNameExists
		}
		return HostedGroup{}, false, err
	}
	for _, member := range group.Members {
		if _, err = tx.ExecContext(ctx, `INSERT INTO hosted_group_members (group_id,repository_id,position) VALUES ($1,$2,$3)`, group.ID, member.RepositoryID, member.Position); err != nil {
			if isUnique(err) {
				return HostedGroup{}, false, ErrNameExists
			}
			return HostedGroup{}, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO hosted_group_idempotency (actor,key,payload_hash,group_id,expires_at) VALUES ($1,$2,$3,$4,now() + interval '24 hours')`, actor, key, payload, group.ID); err != nil {
		return HostedGroup{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return HostedGroup{}, false, err
	}
	return group, false, nil
}

func (s *PostgresStore) ListHostedGroups(ctx context.Context, limit int, after string) ([]HostedGroup, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text, name, format, version::text FROM hosted_groups WHERE ($1='' OR id::text>$1) ORDER BY id LIMIT $2`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	groups := make([]HostedGroup, 0, limit)
	for rows.Next() {
		var group HostedGroup
		if err := rows.Scan(&group.ID, &group.Name, &group.Format, &group.Version); err != nil {
			return nil, "", err
		}
		if err := scanHostedGroupMembers(ctx, s.db, &group); err != nil {
			return nil, "", err
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(groups) > limit {
		next = groups[limit-1].ID
		groups = groups[:limit]
	}
	return groups, next, nil
}

func (s *PostgresStore) GetHostedGroup(ctx context.Context, id string) (HostedGroup, error) {
	return s.getHostedGroup(ctx, s.db, id)
}

func (s *PostgresStore) replaceHostedGroup(ctx context.Context, id, name string, format Format, members []GroupMember, expectedVersion string, replaceMetadata bool) (HostedGroup, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HostedGroup{}, err
	}
	defer tx.Rollback()
	query := `UPDATE hosted_groups SET version=version+1`
	args := []any{id, expectedVersion}
	if replaceMetadata {
		query += `, name=$3, format=$4`
		args = []any{id, expectedVersion, name, format}
	}
	query += ` WHERE id::text=$1 AND version::text=$2 RETURNING id::text,name,format,version::text`
	var group HostedGroup
	err = tx.QueryRowContext(ctx, query, args...).Scan(&group.ID, &group.Name, &group.Format, &group.Version)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.getHostedGroup(ctx, tx, id); errors.Is(getErr, ErrNotFound) {
			return HostedGroup{}, ErrNotFound
		}
		return HostedGroup{}, ErrVersionConflict
	}
	if err != nil {
		return HostedGroup{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM hosted_group_members WHERE group_id::text=$1`, id); err != nil {
		return HostedGroup{}, err
	}
	for _, member := range members {
		if _, err = tx.ExecContext(ctx, `INSERT INTO hosted_group_members (group_id,repository_id,position) VALUES ($1,$2,$3)`, id, member.RepositoryID, member.Position); err != nil {
			return HostedGroup{}, err
		}
	}
	group.Members = append([]GroupMember(nil), members...)
	if err = tx.Commit(); err != nil {
		return HostedGroup{}, err
	}
	return group, nil
}

func (s *PostgresStore) ReplaceHostedGroup(ctx context.Context, group HostedGroup, expectedVersion string) (HostedGroup, error) {
	return s.replaceHostedGroup(ctx, group.ID, group.Name, group.Format, group.Members, expectedVersion, true)
}
func (s *PostgresStore) ReplaceHostedGroupMembers(ctx context.Context, id string, members []GroupMember, expectedVersion string) (HostedGroup, error) {
	current, err := s.GetHostedGroup(ctx, id)
	if err != nil {
		return HostedGroup{}, err
	}
	return s.replaceHostedGroup(ctx, id, current.Name, current.Format, members, expectedVersion, false)
}
func (s *PostgresStore) DeleteHostedGroup(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM hosted_groups WHERE id::text=$1`, id)
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

func loadRepositoryGrants(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, repositoryID, version string) (RepositoryGrantSet, error) {
	rows, err := query.QueryContext(ctx, `SELECT principal, scopes FROM repository_grants WHERE repository_id::text=$1 ORDER BY principal`, repositoryID)
	if err != nil {
		return RepositoryGrantSet{}, err
	}
	defer rows.Close()
	set := RepositoryGrantSet{Version: version, Grants: []RepositoryGrant{}}
	for rows.Next() {
		var grant RepositoryGrant
		if err := rows.Scan(&grant.Principal, &grant.Scopes); err != nil {
			return RepositoryGrantSet{}, err
		}
		set.Grants = append(set.Grants, grant)
	}
	return set, rows.Err()
}

func (s *PostgresStore) GetRepositoryGrants(ctx context.Context, repositoryID string) (RepositoryGrantSet, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryGrantSet{}, err
	}
	if !exists {
		return RepositoryGrantSet{}, ErrNotFound
	}
	var version string
	err := s.db.QueryRowContext(ctx, `SELECT version::text FROM repository_grant_sets WHERE repository_id::text=$1`, repositoryID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryGrantSet{Version: "1", Grants: []RepositoryGrant{}}, nil
	}
	if err != nil {
		return RepositoryGrantSet{}, err
	}
	return loadRepositoryGrants(ctx, s.db, repositoryID, version)
}

func (s *PostgresStore) ReplaceRepositoryGrants(ctx context.Context, repositoryID string, grants []RepositoryGrant, expectedVersion string) (RepositoryGrantSet, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RepositoryGrantSet{}, err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryGrantSet{}, err
	}
	if !exists {
		return RepositoryGrantSet{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_grant_sets (repository_id,version) VALUES ($1,1) ON CONFLICT DO NOTHING`, repositoryID); err != nil {
		return RepositoryGrantSet{}, err
	}
	var version string
	err = tx.QueryRowContext(ctx, `UPDATE repository_grant_sets SET version=version+1 WHERE repository_id::text=$1 AND version::text=$2 RETURNING version::text`, repositoryID, expectedVersion).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryGrantSet{}, ErrVersionConflict
	}
	if err != nil {
		return RepositoryGrantSet{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM repository_grants WHERE repository_id::text=$1`, repositoryID); err != nil {
		return RepositoryGrantSet{}, err
	}
	for _, grant := range grants {
		if _, err = tx.ExecContext(ctx, `INSERT INTO repository_grants (repository_id,principal,scopes) VALUES ($1,$2,$3)`, repositoryID, grant.Principal, grant.Scopes); err != nil {
			return RepositoryGrantSet{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return RepositoryGrantSet{}, err
	}
	return RepositoryGrantSet{Version: version, Grants: append([]RepositoryGrant{}, grants...)}, nil
}

func (s *PostgresStore) GetRepositoryRetentionPolicy(ctx context.Context, repositoryID string) (RepositoryRetentionPolicy, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	if !exists {
		return RepositoryRetentionPolicy{}, ErrNotFound
	}
	policy := defaultRepositoryRetentionPolicy()
	err := s.db.QueryRowContext(ctx, `SELECT version::text,keep_days,minimum_versions FROM repository_retention_policies WHERE repository_id::text=$1`, repositoryID).Scan(&policy.Version, &policy.KeepDays, &policy.MinimumVersions)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	return policy, err
}

func (s *PostgresStore) ReplaceRepositoryRetentionPolicy(ctx context.Context, repositoryID string, policy RepositoryRetentionPolicy, expectedVersion string) (RepositoryRetentionPolicy, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_repositories WHERE id::text=$1)`, repositoryID).Scan(&exists); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	if !exists {
		return RepositoryRetentionPolicy{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repository_retention_policies (repository_id,version,keep_days,minimum_versions) VALUES ($1,1,30,1) ON CONFLICT DO NOTHING`, repositoryID); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE repository_retention_policies SET version=version+1, keep_days=$3, minimum_versions=$4 WHERE repository_id::text=$1 AND version::text=$2 RETURNING version::text`, repositoryID, expectedVersion, policy.KeepDays, policy.MinimumVersions).Scan(&policy.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return RepositoryRetentionPolicy{}, ErrVersionConflict
	}
	if err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	if err = tx.Commit(); err != nil {
		return RepositoryRetentionPolicy{}, err
	}
	return policy, nil
}

func (s *PostgresStore) CreateOCIUpload(ctx context.Context, v OCIUpload) (OCIUpload, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO native_oci_uploads (id,repository_id,name,object_key,byte_offset,state,expires_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, v.ID, v.RepositoryID, v.Name, v.ObjectKey, v.Offset, v.State, v.ExpiresAt)
	return v, err
}
func (s *PostgresStore) StageOCIObjectIntent(ctx context.Context, intent OCIObjectIntent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO native_oci_object_intents (object_key,digest,size) VALUES ($1,$2,$3) ON CONFLICT (object_key) DO NOTHING`, intent.ObjectKey, intent.Digest, intent.Size)
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
	defer tx.Rollback()
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
	defer tx.Rollback()
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
	defer rows.Close()
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
	defer rows.Close()
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
	rows, err := s.db.QueryContext(ctx, `SELECT object_key,digest,size,created_at,claimed_at,collected_at FROM native_oci_object_intents WHERE created_at < $1 AND claimed_at IS NULL AND collected_at IS NULL ORDER BY created_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var intents []OCIObjectIntent
	for rows.Next() {
		var intent OCIObjectIntent
		if err = rows.Scan(&intent.ObjectKey, &intent.Digest, &intent.Size, &intent.CreatedAt, &intent.ClaimedAt, &intent.CollectedAt); err != nil {
			return nil, err
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
	defer tx.Rollback()
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
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_oci_manifests (repository_id,name,digest,object_key,media_type,size) VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (repository_id,name,digest) DO NOTHING`, v.RepositoryID, v.Name, v.Digest, v.ObjectKey, v.MediaType, v.Size); err != nil {
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
	defer rows.Close()
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
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_oci_tags WHERE repository_id::text=$1 AND name=$2 AND digest=$3`, repositoryID, name, digest); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_oci_object_intents SET claimed_at=NULL,collected_at=NULL,created_at=now() WHERE object_key=(SELECT object_key FROM native_oci_manifests WHERE repository_id::text=$1 AND name=$2 AND digest=$3)`, repositoryID, name, digest); err != nil {
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

func (s *PostgresStore) PutRawAsset(ctx context.Context, v RawAsset) (RawAsset, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_raw_objects (digest,object_key,size) VALUES ($1,$2,$3) ON CONFLICT (digest) DO UPDATE SET collected_at=NULL`, v.Digest, v.ObjectKey, v.Size); err != nil {
		return v, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT object_key,size FROM native_raw_objects WHERE digest=$1`, v.Digest).Scan(&v.ObjectKey, &v.Size); err != nil {
		return v, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_raw_assets (repository_id,path,digest,content_type) VALUES ($1,$2,$3,$4) ON CONFLICT (repository_id,path) DO UPDATE SET digest=EXCLUDED.digest,content_type=EXCLUDED.content_type,updated_at=now()`, v.RepositoryID, v.Path, v.Digest, v.ContentType); err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func (s *PostgresStore) StageRawObject(ctx context.Context, object RawObject) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO native_raw_objects (digest,object_key,size) VALUES ($1,$2,$3) ON CONFLICT (digest) DO UPDATE SET collected_at=NULL`, object.Digest, object.ObjectKey, object.Size)
	return err
}
func (s *PostgresStore) LockRawObject(ctx context.Context, digest string) (func(), error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, "native-raw-object:"+digest); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "native-raw-object:"+digest)
		_ = conn.Close()
	}, nil
}
func (s *PostgresStore) GetRawAsset(ctx context.Context, repositoryID, path string) (RawAsset, error) {
	var v RawAsset
	err := s.db.QueryRowContext(ctx, `SELECT a.repository_id::text,a.path,a.digest,o.object_key,o.size,a.content_type FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest WHERE a.repository_id::text=$1 AND a.path=$2`, repositoryID, path).Scan(&v.RepositoryID, &v.Path, &v.Digest, &v.ObjectKey, &v.Size, &v.ContentType)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) DeleteRawAsset(ctx context.Context, repositoryID, path string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM native_raw_assets WHERE repository_id::text=$1 AND path=$2`, repositoryID, path)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) ListUnreferencedRawObjects(ctx context.Context, before time.Time, limit int) ([]RawObject, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT o.digest,o.object_key FROM native_raw_objects o WHERE o.created_at < $1 AND o.collected_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_raw_assets a WHERE a.digest=o.digest) ORDER BY o.created_at LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var objects []RawObject
	for rows.Next() {
		var object RawObject
		if err = rows.Scan(&object.Digest, &object.ObjectKey); err != nil {
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
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at FROM native_maven_artifacts WHERE repository_id::text=$1 AND state='visible' ORDER BY created_at DESC`, repoID)
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
func (s *PostgresStore) GetMavenArtifact(ctx context.Context, repositoryID, artifactID string) (MavenArtifact, error) {
	var artifact MavenArtifact
	err := s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,coordinate,digest,state,created_at FROM native_maven_artifacts WHERE repository_id::text=$1 AND id::text=$2`, repositoryID, artifactID).Scan(&artifact.ID, &artifact.RepositoryID, &artifact.Coordinate, &artifact.Digest, &artifact.State, &artifact.CreatedAt)
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
	defer tx.Rollback()
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
	prefix := mavenArtifactPathPrefix(artifact.Coordinate)
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_maven_assets WHERE repository_id::text=$1 AND left(path, length($2))=$2`, repositoryID, prefix); err != nil {
		return MavenArtifact{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_maven_object_references r WHERE NOT EXISTS (SELECT 1 FROM native_maven_assets a WHERE a.object_key=r.object_key)`); err != nil {
		return MavenArtifact{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_artifacts SET state='deleted' WHERE id::text=$1`, artifactID); err != nil {
		return MavenArtifact{}, err
	}
	artifact.State = "deleted"
	if err = tx.Commit(); err != nil {
		return MavenArtifact{}, err
	}
	return artifact, nil
}
func (s *PostgresStore) ClaimExpiredMavenObjectIntents(ctx context.Context, before time.Time, limit int) ([]MavenObjectIntent, error) {
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (SELECT i.object_key FROM native_maven_object_intents i JOIN native_maven_publish_sessions s ON s.id=i.session_id WHERE i.created_at <= $1 AND (i.claimed_at IS NULL OR i.claimed_at <= now() - interval '5 minutes') AND i.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM native_maven_object_references r WHERE r.object_key=i.object_key) AND NOT (s.state='open' AND s.expires_at > now()) ORDER BY i.created_at FOR UPDATE OF s, i SKIP LOCKED LIMIT $2) UPDATE native_maven_object_intents i SET claimed_at=now(), claimed_token=md5(random()::text || clock_timestamp()::text || i.object_key) FROM candidates WHERE i.object_key=candidates.object_key RETURNING i.object_key, i.claimed_token`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MavenObjectIntent{}
	for rows.Next() {
		var v MavenObjectIntent
		if err := rows.Scan(&v.ObjectKey, &v.ClaimToken); err != nil {
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO resolver_audit_log (group_name, repository, member_name, outcome, actor, occurred_at, format, resource, representation, member_type, upstream_host, operation, http_status, cache_disposition, bytes, authorization_source, authorization_reason, request_id, trace_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`, audit.GroupName, audit.Repository, audit.MemberName, audit.Outcome, audit.Actor, audit.OccurredAt, audit.Format, audit.Resource, audit.Representation, audit.MemberType, audit.UpstreamHost, audit.Operation, audit.Status, audit.CacheDisposition, audit.Bytes, audit.AuthorizationSource, audit.AuthorizationReason, audit.RequestID, audit.TraceID)
	return err
}

func (s *PostgresStore) ListAudits(ctx context.Context, query AuditQuery) ([]AuditRecord, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT group_name, repository, member_name, outcome, actor, occurred_at,
		COALESCE(format, ''), COALESCE(resource, ''), COALESCE(representation, ''), COALESCE(member_type, ''), COALESCE(upstream_host, ''), COALESCE(operation, ''),
		COALESCE(http_status, 0), COALESCE(cache_disposition, ''), COALESCE(bytes, 0), COALESCE(authorization_source, ''), COALESCE(authorization_reason, ''), COALESCE(request_id, ''), COALESCE(trace_id, '')
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
		if err := rows.Scan(&audit.GroupName, &audit.Repository, &audit.MemberName, &audit.Outcome, &audit.Actor, &audit.OccurredAt, &audit.Format, &audit.Resource, &audit.Representation, &audit.MemberType, &audit.UpstreamHost, &audit.Operation, &audit.Status, &audit.CacheDisposition, &audit.Bytes, &audit.AuthorizationSource, &audit.AuthorizationReason, &audit.RequestID, &audit.TraceID); err != nil {
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO conan_group_members (group_name, name, member_type, endpoint, position, anonymous, allowed_hosts, repository_id) VALUES ($1,$2,$3,$4,$5,$6,COALESCE($7::text[], '{}'::text[]),NULLIF($8,'')::uuid)`, group.Name, member.Name, member.Type, member.Endpoint, member.Position, member.Anonymous, member.AllowedHosts, member.RepositoryID); err != nil {
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
	rows, err := s.db.QueryContext(ctx, `SELECT name, member_type, endpoint, position, anonymous, array_to_json(allowed_hosts), COALESCE(repository_id::text, '') FROM conan_group_members WHERE group_name=$1 ORDER BY position`, name)
	if err != nil {
		return Group{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var member Member
		var allowedHosts []byte
		if err := rows.Scan(&member.Name, &member.Type, &member.Endpoint, &member.Position, &member.Anonymous, &allowedHosts, &member.RepositoryID); err != nil {
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
