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
