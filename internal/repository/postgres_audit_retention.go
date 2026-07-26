package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

func (s *PostgresStore) GetAuditRetentionPolicy(ctx context.Context) (AuditRetentionPolicy, error) {
	var p AuditRetentionPolicy
	var version int64
	err := s.db.QueryRowContext(ctx, `SELECT version,enabled,keep_days FROM audit_retention_policy WHERE singleton=true`).Scan(&version, &p.Enabled, &p.KeepDays)
	p.Version = strconv.FormatInt(version, 10)
	return p, err
}
func (s *PostgresStore) ReplaceAuditRetentionPolicy(ctx context.Context, p AuditRetentionPolicy, expected string) (AuditRetentionPolicy, error) {
	v, err := strconv.ParseInt(expected, 10, 64)
	if err != nil {
		return AuditRetentionPolicy{}, ErrVersionConflict
	}
	var next int64
	err = s.db.QueryRowContext(ctx, `UPDATE audit_retention_policy SET enabled=$1,keep_days=$2,version=version+1 WHERE singleton=true AND version=$3 RETURNING version`, p.Enabled, p.KeepDays, v).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return AuditRetentionPolicy{}, ErrVersionConflict
	}
	if err != nil {
		return AuditRetentionPolicy{}, err
	}
	p.Version = strconv.FormatInt(next, 10)
	return p, nil
}
func (s *PostgresStore) EnqueueAuditCleanupJob(ctx context.Context, j AuditCleanupJob) (AuditCleanupJob, bool, error) {
	err := scanAuditCleanupJob(s.db.QueryRowContext(ctx, `INSERT INTO audit_cleanup_jobs (id,idempotency_key,policy_version,cutoff_at,batch_size,state) VALUES ($1,$2,$3,$4,$5,'pending') ON CONFLICT (idempotency_key) DO NOTHING RETURNING id::text,idempotency_key,policy_version,cutoff_at,batch_size,deleted,state,created_at,started_at,completed_at,last_error`, j.ID, j.IdempotencyKey, j.PolicyVersion, j.CutoffAt, j.BatchSize), &j)
	if err == nil {
		return j, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AuditCleanupJob{}, false, err
	}
	var existing AuditCleanupJob
	if err = scanAuditCleanupJob(s.db.QueryRowContext(ctx, `SELECT id::text,idempotency_key,policy_version,cutoff_at,batch_size,deleted,state,created_at,started_at,completed_at,last_error FROM audit_cleanup_jobs WHERE idempotency_key=$1`, j.IdempotencyKey), &existing); err != nil {
		return AuditCleanupJob{}, false, err
	}
	if existing.PolicyVersion != j.PolicyVersion || existing.BatchSize != j.BatchSize {
		return AuditCleanupJob{}, false, ErrIdempotencyConflict
	}
	return existing, true, nil
}
func (s *PostgresStore) ListAuditCleanupJobs(ctx context.Context, limit int) ([]AuditCleanupJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,idempotency_key,policy_version,cutoff_at,batch_size,deleted,state,created_at,started_at,completed_at,last_error FROM audit_cleanup_jobs ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []AuditCleanupJob{}
	for rows.Next() {
		var j AuditCleanupJob
		if err := scanAuditCleanupJob(rows, &j); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
func (s *PostgresStore) ClaimAuditCleanupJobs(ctx context.Context, limit int) ([]AuditCleanupJob, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
        SELECT id FROM audit_cleanup_jobs
        WHERE state IN ('pending','failed') OR (state='running' AND started_at < now() - interval '15 minutes')
        ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1
    ) UPDATE audit_cleanup_jobs j SET state='running',started_at=now(),completed_at=NULL,last_error=''
    FROM candidates WHERE j.id=candidates.id
    RETURNING j.id::text,j.idempotency_key,j.policy_version,j.cutoff_at,j.batch_size,j.deleted,j.state,j.created_at,j.started_at,j.completed_at,j.last_error`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []AuditCleanupJob{}
	for rows.Next() {
		var j AuditCleanupJob
		if err := scanAuditCleanupJob(rows, &j); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
func (s *PostgresStore) CompleteAuditCleanupJob(ctx context.Context, id string, deleted int) error {
	return s.finishAuditCleanupJob(ctx, id, LifecycleJobCompleted, "", deleted)
}
func (s *PostgresStore) FailAuditCleanupJob(ctx context.Context, id, message string) error {
	return s.finishAuditCleanupJob(ctx, id, LifecycleJobFailed, message, 0)
}
func (s *PostgresStore) finishAuditCleanupJob(ctx context.Context, id string, state LifecycleJobState, message string, deleted int) error {
	r, err := s.db.ExecContext(ctx, `UPDATE audit_cleanup_jobs SET state=$2,completed_at=now(),last_error=$3,deleted=CASE WHEN $2='completed' THEN $4 ELSE deleted END WHERE id::text=$1 AND state='running'`, id, state, message, deleted)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}
func (s *PostgresStore) DeleteAuditsBefore(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	r, err := s.db.ExecContext(ctx, `WITH candidates AS (SELECT id FROM resolver_audit_log WHERE occurred_at < $1 ORDER BY occurred_at,id FOR UPDATE SKIP LOCKED LIMIT $2) DELETE FROM resolver_audit_log WHERE id IN (SELECT id FROM candidates)`, cutoff, limit)
	if err != nil {
		return 0, err
	}
	n, err := r.RowsAffected()
	return int(n), err
}

type auditCleanupScanner interface{ Scan(...any) error }

func scanAuditCleanupJob(s auditCleanupScanner, j *AuditCleanupJob) error {
	var version int64
	var started, completed sql.NullTime
	if err := s.Scan(&j.ID, &j.IdempotencyKey, &version, &j.CutoffAt, &j.BatchSize, &j.Deleted, &j.State, &j.CreatedAt, &started, &completed, &j.LastError); err != nil {
		return err
	}
	j.PolicyVersion = strconv.FormatInt(version, 10)
	if started.Valid {
		j.StartedAt = started.Time
	}
	if completed.Valid {
		j.CompletedAt = completed.Time
	}
	return nil
}
