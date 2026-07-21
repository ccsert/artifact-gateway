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

func (s *PostgresStore) RecordAudit(ctx context.Context, audit AuditRecord) error {
	if audit.OccurredAt.IsZero() {
		audit.OccurredAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO resolver_audit_log (group_name, repository, member_name, outcome, actor, occurred_at, format, resource, representation, member_type, upstream_host, operation, http_status, cache_disposition, bytes) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, audit.GroupName, audit.Repository, audit.MemberName, audit.Outcome, audit.Actor, audit.OccurredAt, audit.Format, audit.Resource, audit.Representation, audit.MemberType, audit.UpstreamHost, audit.Operation, audit.Status, audit.CacheDisposition, audit.Bytes)
	return err
}

func (s *PostgresStore) ListAudits(ctx context.Context, query AuditQuery) ([]AuditRecord, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT group_name, repository, member_name, outcome, actor, occurred_at,
		COALESCE(format, ''), COALESCE(resource, ''), COALESCE(representation, ''), COALESCE(member_type, ''), COALESCE(upstream_host, ''), COALESCE(operation, ''),
		COALESCE(http_status, 0), COALESCE(cache_disposition, ''), COALESCE(bytes, 0)
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
		if err := rows.Scan(&audit.GroupName, &audit.Repository, &audit.MemberName, &audit.Outcome, &audit.Actor, &audit.OccurredAt, &audit.Format, &audit.Resource, &audit.Representation, &audit.MemberType, &audit.UpstreamHost, &audit.Operation, &audit.Status, &audit.CacheDisposition, &audit.Bytes); err != nil {
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
	normalizeGroup(&group)
	err = tx.QueryRowContext(ctx, `INSERT INTO conan_groups (name, enabled, anonymous) VALUES ($1, true, $2) RETURNING created_at`, group.Name, group.Anonymous).Scan(&group.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return Group{}, ErrNameExists
		}
		return Group{}, err
	}
	for _, member := range group.Members {
		if _, err := tx.ExecContext(ctx, `INSERT INTO conan_group_members (group_name, name, member_type, endpoint, position, anonymous) VALUES ($1,$2,$3,$4,$5,$6)`, group.Name, member.Name, member.Type, member.Endpoint, member.Position, member.Anonymous); err != nil {
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
	if err := s.db.QueryRowContext(ctx, `SELECT name, enabled, anonymous, created_at FROM conan_groups WHERE name=$1`, name).Scan(&group.Name, &group.Enabled, &group.Anonymous, &group.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Group{}, ErrNotFound
		}
		return Group{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, member_type, endpoint, position, anonymous FROM conan_group_members WHERE group_name=$1 ORDER BY position`, name)
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
