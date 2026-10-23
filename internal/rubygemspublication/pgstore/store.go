// Package pgstore is the PostgreSQL adapter for RubyGems publication records.
package pgstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	protocol "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/requestcontext"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

type Store struct{ db, locks *sql.DB }

// New uses caller-owned pools. The dedicated lock pool must not be the same
// single-connection pool used by transactional publication queries.
func New(db, locks *sql.DB) (*Store, error) {
	if db == nil || locks == nil {
		return nil, errors.New("RubyGems database and advisory-lock pools are required")
	}
	return &Store{db, locks}, nil
}

const columns = `id::text,repository_id::text,name,version,platform,digest,size,actor,state,object_key,metadata_json,metadata_yaml,created_at,expires_at,committed_at,collected_at,governance_version::text,yanked,governance_updated_at,governance_updated_by,governance_reason`

type scanner interface{ Scan(...any) error }

func scan(row scanner) (domain.Session, error) {
	var s domain.Session
	var committed, collected, governed sql.NullTime
	err := row.Scan(&s.ID, &s.RepositoryID, &s.Name, &s.Version, &s.Platform, &s.Digest, &s.Size, &s.Actor, &s.State, &s.ObjectKey, &s.MetadataJSON, &s.MetadataYAML, &s.CreatedAt, &s.ExpiresAt, &committed, &collected, &s.Governance.Version, &s.Governance.Yanked, &governed, &s.Governance.UpdatedBy, &s.Governance.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		err = repository.ErrNotFound
	}
	if committed.Valid {
		s.CommittedAt = committed.Time
	}
	if collected.Valid {
		s.CollectedAt = collected.Time
	}
	if governed.Valid {
		s.Governance.UpdatedAt = governed.Time
	}
	return s, err
}
func (s *Store) Lock(ctx context.Context, repo, identity string) (context.Context, func(), error) {
	conn, e := s.locks.Conn(ctx)
	if e != nil {
		return ctx, func() {}, e
	}
	sum := sha256.Sum256([]byte(repo + "\x00" + identity))
	key := "native-rubygems:" + hex.EncodeToString(sum[:])
	_, e = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key)
	if e != nil {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
		return ctx, func() {}, e
	}
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); err != nil {
				_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			_ = conn.Close()
		})
	}, nil
}
func (s *Store) transaction(ctx context.Context, id string) (*sql.Tx, string, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, "", e
	}
	var name string
	e = tx.QueryRowContext(ctx, `SELECT name FROM hosted_repositories WHERE id::text=$1 AND format='rubygems' AND repo_type='hosted' AND state='active' FOR UPDATE`, id).Scan(&name)
	if e != nil {
		_ = tx.Rollback()
		if errors.Is(e, sql.ErrNoRows) {
			e = repository.ErrNotFound
		}
		return nil, "", e
	}
	return tx, name, nil
}
func capacity(ctx context.Context, tx *sql.Tx, repo string) (domain.Capacity, error) {
	var c domain.Capacity
	e := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size) FILTER(WHERE state='committed'),0),COALESCE(SUM(size) FILTER(WHERE state='pending' AND expires_at>now()),0),COUNT(*) FILTER(WHERE state='committed'),COALESCE((SELECT quota_bytes FROM native_rubygems_quotas WHERE repository_id::text=$1),0) FROM native_rubygems_sessions WHERE repository_id::text=$1`, repo).Scan(&c.UsedBytes, &c.PendingBytes, &c.ObjectCount, &c.QuotaBytes)
	return c, e
}
func (s *Store) Reserve(ctx context.Context, in domain.Session) (domain.Session, bool, error) {
	if !domain.ValidInput(in) || !in.ExpiresAt.After(time.Now()) || in.State != "" || in.ObjectKey != "" || !in.CreatedAt.IsZero() || !in.CommittedAt.IsZero() || !in.CollectedAt.IsZero() || in.Governance != (domain.Governance{}) {
		return domain.Session{}, false, domain.ErrInvalid
	}
	tx, _, e := s.transaction(ctx, in.RepositoryID)
	if e != nil {
		return domain.Session{}, false, e
	}
	defer func() { _ = tx.Rollback() }()
	if _, e = tx.ExecContext(ctx, `UPDATE native_rubygems_sessions SET state='expired' WHERE repository_id::text=$1 AND state='pending' AND expires_at<=now()`, in.RepositoryID); e != nil {
		return domain.Session{}, false, e
	}
	prior, e := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE id::text=$1`, in.ID))
	if e == nil {
		if prior.Governance.Yanked || !domain.Same(prior, in) || prior.Actor != in.Actor {
			return domain.Session{}, false, domain.ErrConflict
		}
		if prior.State == "expired" {
			return domain.Session{}, false, domain.ErrExpired
		}
		return prior, true, tx.Commit()
	}
	if !errors.Is(e, repository.ErrNotFound) {
		return domain.Session{}, false, e
	}
	prior, e = scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE repository_id::text=$1 AND ((name=$2 AND canonical_version=$3 AND platform=$4) OR full_name=$5) AND state IN ('pending','committed')`, in.RepositoryID, in.Name, protocol.CanonicalVersion(in.Version), in.Platform, domain.Visible(in).FullName()))
	if e == nil {
		if prior.Governance.Yanked || !domain.Same(prior, in) || prior.State == "pending" && prior.Actor != in.Actor {
			return domain.Session{}, false, domain.ErrConflict
		}
		return prior, true, tx.Commit()
	}
	if !errors.Is(e, repository.ErrNotFound) {
		return domain.Session{}, false, e
	}
	c, e := capacity(ctx, tx, in.RepositoryID)
	if e != nil {
		return domain.Session{}, false, e
	}
	if c.QuotaBytes > 0 && (c.UsedBytes+c.PendingBytes > c.QuotaBytes || in.Size > c.QuotaBytes-c.UsedBytes-c.PendingBytes) {
		return domain.Session{}, false, repository.ErrQuotaExceeded
	}
	var count int
	e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM native_rubygems_sessions WHERE repository_id::text=$1 AND state IN ('pending','committed')`, in.RepositoryID).Scan(&count)
	if e != nil {
		return domain.Session{}, false, e
	}
	if count >= protocol.MaxIndexEntries {
		return domain.Session{}, false, repository.ErrQuotaExceeded
	}
	in.ObjectKey = domain.ObjectKey(in)
	p := domain.Visible(in)
	in, e = scan(tx.QueryRowContext(ctx, `INSERT INTO native_rubygems_sessions(id,repository_id,name,version,canonical_version,platform,full_name,digest,size,actor,state,object_key,metadata_json,metadata_yaml,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'pending',$11,$12,$13,$14) RETURNING `+columns, in.ID, in.RepositoryID, in.Name, in.Version, protocol.CanonicalVersion(in.Version), in.Platform, p.FullName(), in.Digest, in.Size, in.Actor, in.ObjectKey, string(in.MetadataJSON), in.MetadataYAML, in.ExpiresAt))
	if e != nil {
		return domain.Session{}, false, e
	}
	return in, false, tx.Commit()
}
func (s *Store) Commit(ctx context.Context, id, actor string) (domain.Publication, bool, error) {
	var repo string
	e := s.db.QueryRowContext(ctx, `SELECT repository_id::text FROM native_rubygems_sessions WHERE id::text=$1`, id).Scan(&repo)
	if errors.Is(e, sql.ErrNoRows) {
		e = repository.ErrNotFound
	}
	if e != nil {
		return domain.Publication{}, false, e
	}
	tx, name, e := s.transaction(ctx, repo)
	if e != nil {
		return domain.Publication{}, false, e
	}
	defer func() { _ = tx.Rollback() }()
	in, e := scan(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE id::text=$1 FOR UPDATE`, id))
	if e != nil {
		return domain.Publication{}, false, e
	}
	if in.State == "committed" {
		if in.Governance.Yanked {
			return domain.Publication{}, false, domain.ErrConflict
		}
		return domain.Visible(in), true, tx.Commit()
	}
	if in.State != "pending" || !in.ExpiresAt.After(time.Now()) {
		return domain.Publication{}, false, domain.ErrExpired
	}
	if in.Actor != actor {
		return domain.Publication{}, false, domain.ErrConflict
	}
	in, e = scan(tx.QueryRowContext(ctx, `UPDATE native_rubygems_sessions SET state='committed',committed_at=now() WHERE id::text=$1 RETURNING `+columns, id))
	if e != nil {
		return domain.Publication{}, false, e
	}
	p := domain.Visible(in)
	if e = insertAudit(ctx, tx, domain.Audit(p, name)); e != nil {
		return domain.Publication{}, false, e
	}
	return p, false, tx.Commit()
}
func (s *Store) Get(ctx context.Context, repo, fullName string) (domain.Publication, error) {
	in, e := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE repository_id::text=$1 AND full_name=$2 AND state='committed' AND NOT yanked AND EXISTS(SELECT 1 FROM hosted_repositories r WHERE r.id=repository_id AND r.format='rubygems' AND r.repo_type='hosted' AND r.state='active')`, repo, fullName))
	return domain.Visible(in), e
}
func (s *Store) List(ctx context.Context, repo string, limit int) ([]domain.Publication, error) {
	if limit < 1 || limit > 4097 {
		limit = 4097
	}
	rows, e := s.db.QueryContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE repository_id::text=$1 AND state='committed' AND NOT yanked AND EXISTS(SELECT 1 FROM hosted_repositories r WHERE r.id=repository_id AND r.format='rubygems' AND r.repo_type='hosted' AND r.state='active') ORDER BY full_name COLLATE "C" LIMIT $2`, repo, limit)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Publication{}
	for rows.Next() {
		in, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, domain.Visible(in))
	}
	return out, rows.Err()
}
func (s *Store) GetSession(ctx context.Context, id string) (domain.Session, error) {
	return scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM native_rubygems_sessions WHERE id::text=$1`, id))
}
func (s *Store) Expire(ctx context.Context, now time.Time, limit int) ([]domain.Session, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	rows, e := s.db.QueryContext(ctx, `WITH candidates AS (SELECT id FROM native_rubygems_sessions WHERE collected_at IS NULL AND (state='expired' OR state='pending' AND expires_at<=$1) ORDER BY expires_at,id FOR UPDATE SKIP LOCKED LIMIT $2) UPDATE native_rubygems_sessions SET state='expired' WHERE id IN(SELECT id FROM candidates) RETURNING `+columns, now, limit)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	out := []domain.Session{}
	for rows.Next() {
		in, e := scan(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, in)
	}
	return out, rows.Err()
}
func (s *Store) MarkCollected(ctx context.Context, id, key string) error {
	r, e := s.db.ExecContext(ctx, `UPDATE native_rubygems_sessions SET collected_at=COALESCE(collected_at,now()) WHERE id::text=$1 AND object_key=$2 AND state='expired'`, id, key)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e == nil && n != 1 {
		e = domain.ErrState
	}
	return e
}
func (s *Store) Capacity(ctx context.Context, repo string) (domain.Capacity, error) {
	tx, _, e := s.transaction(ctx, repo)
	if e != nil {
		return domain.Capacity{}, e
	}
	defer func() { _ = tx.Rollback() }()
	return capacity(ctx, tx, repo)
}
func (s *Store) SetQuota(ctx context.Context, repo, actor string, quota int64) (domain.Capacity, error) {
	if quota < 0 {
		return domain.Capacity{}, domain.ErrInvalid
	}
	tx, name, e := s.transaction(ctx, repo)
	if e != nil {
		return domain.Capacity{}, e
	}
	defer func() { _ = tx.Rollback() }()
	c, e := capacity(ctx, tx, repo)
	if e != nil {
		return c, e
	}
	if quota > 0 && quota < c.UsedBytes+c.PendingBytes {
		return c, repository.ErrQuotaExceeded
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO native_rubygems_quotas(repository_id,quota_bytes)VALUES($1,$2) ON CONFLICT(repository_id) DO UPDATE SET quota_bytes=EXCLUDED.quota_bytes`, repo, quota)
	if e != nil {
		return c, e
	}
	e = insertAudit(ctx, tx, repository.AuditRecord{Actor: actor, Repository: name, GroupName: name, Format: "rubygems", Resource: repo, Operation: "rubygems.quota.configure", Outcome: repository.AuditResolved, Status: 200, OccurredAt: time.Now().UTC()})
	if e != nil {
		return c, e
	}
	c.QuotaBytes = quota
	return c, tx.Commit()
}
func insertAudit(ctx context.Context, tx *sql.Tx, a repository.AuditRecord) error {
	evidence, e := json.Marshal(a.Evidence)
	if e != nil {
		return e
	}
	if a.Evidence == nil {
		evidence = []byte("{}")
	}
	if ids, ok := requestcontext.FromContext(ctx); ok {
		a.RequestID = ids.RequestID
		a.TraceID = ids.TraceID
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO resolver_audit_log(group_name,repository,member_name,outcome,actor,occurred_at,format,resource,representation,member_type,upstream_host,operation,http_status,cache_disposition,bytes,authorization_source,authorization_reason,request_id,trace_id,evidence)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20::jsonb)`, a.GroupName, a.Repository, a.MemberName, a.Outcome, a.Actor, a.OccurredAt, a.Format, a.Resource, a.Representation, a.MemberType, a.UpstreamHost, a.Operation, a.Status, a.CacheDisposition, a.Bytes, a.AuthorizationSource, a.AuthorizationReason, a.RequestID, a.TraceID, evidence)
	return e
}

var _ domain.Store = (*Store)(nil)
