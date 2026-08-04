package repository

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

func (s *PostgresStore) CreateReplicationPlan(ctx context.Context, plan ReplicationPlan, checkpoints []ReplicationCheckpoint) (ReplicationPlan, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReplicationPlan{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	err = scanReplicationPlan(tx.QueryRowContext(ctx, `
		INSERT INTO replication_plans (id, source_repository_id, target_repository_id, format, idempotency_key, state, next_attempt_at, max_attempts)
		VALUES ($1,$2,$3,$4,$5,'pending',now(),LEAST(GREATEST(COALESCE(NULLIF($6, 0), 3), 1), 10))
		ON CONFLICT (target_repository_id, idempotency_key) DO NOTHING
		RETURNING id::text,source_repository_id::text,target_repository_id::text,format,idempotency_key,state,created_at,started_at,completed_at,last_error,next_attempt_at,lease_expires_at,lease_token,attempts,max_attempts`,
		plan.ID, plan.SourceRepositoryID, plan.TargetRepositoryID, plan.Format, plan.IdempotencyKey, plan.MaxAttempts), &plan)
	if err == nil {
		for _, checkpoint := range checkpoints {
			sourceObjectKey := checkpoint.SourceObjectKey
			if sourceObjectKey == "" {
				sourceObjectKey = checkpoint.ObjectKey
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO replication_checkpoints (plan_id,source_object_key,object_key,digest,size,byte_offset,state,attempts,last_error) VALUES ($1,$2,$3,$4,$5,0,'pending',0,'')`, plan.ID, sourceObjectKey, checkpoint.ObjectKey, checkpoint.Digest, checkpoint.Size); err != nil {
				return ReplicationPlan{}, false, err
			}
		}
		if err = tx.Commit(); err != nil {
			return ReplicationPlan{}, false, err
		}
		return plan, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ReplicationPlan{}, false, err
	}

	var existing ReplicationPlan
	if err = scanReplicationPlan(tx.QueryRowContext(ctx, `SELECT id::text,source_repository_id::text,target_repository_id::text,format,idempotency_key,state,created_at,started_at,completed_at,last_error,next_attempt_at,lease_expires_at,lease_token,attempts,max_attempts FROM replication_plans WHERE target_repository_id::text=$1 AND idempotency_key=$2 FOR UPDATE`, plan.TargetRepositoryID, plan.IdempotencyKey), &existing); err != nil {
		return ReplicationPlan{}, false, err
	}
	existingChecks, err := listReplicationCheckpoints(ctx, tx, existing.ID)
	if err != nil {
		return ReplicationPlan{}, false, err
	}
	if existing.SourceRepositoryID != plan.SourceRepositoryID || existing.TargetRepositoryID != plan.TargetRepositoryID || existing.Format != plan.Format || !equivalentReplicationCheckpoints(existingChecks, checkpoints) {
		return ReplicationPlan{}, false, ErrIdempotencyConflict
	}
	if err = tx.Commit(); err != nil {
		return ReplicationPlan{}, false, err
	}
	return existing, true, nil
}

func (s *PostgresStore) ClaimReplicationPlans(ctx context.Context, limit int) ([]ReplicationPlan, error) {
	return s.claimReplicationPlans(ctx, "", limit)
}

func (s *PostgresStore) ClaimReplicationPlansByFormat(ctx context.Context, format Format, limit int) ([]ReplicationPlan, error) {
	return s.claimReplicationPlans(ctx, format, limit)
}

func (s *PostgresStore) claimReplicationPlans(ctx context.Context, format Format, limit int) ([]ReplicationPlan, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	if _, err := s.RecoverExpiredReplicationPlans(ctx, time.Now().UTC()); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		WITH candidates AS (
			SELECT id FROM replication_plans WHERE state IN ('pending','failed') AND attempts < CASE WHEN max_attempts < 1 THEN 3 ELSE max_attempts END AND (next_attempt_at IS NULL OR next_attempt_at <= now()) AND ($1='' OR format=$1) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $2
		)
		UPDATE replication_plans p SET state='running',started_at=now(),completed_at=NULL,last_error='',next_attempt_at=NULL,lease_expires_at=now()+interval '10 minutes',lease_token=md5(random()::text || clock_timestamp()::text || p.id::text),attempts=attempts+1,max_attempts=CASE WHEN max_attempts < 1 THEN 3 ELSE max_attempts END
		FROM candidates WHERE p.id=candidates.id
		RETURNING p.id::text,p.source_repository_id::text,p.target_repository_id::text,p.format,p.idempotency_key,p.state,p.created_at,p.started_at,p.completed_at,p.last_error,p.next_attempt_at,p.lease_expires_at,p.lease_token,p.attempts,p.max_attempts`, format, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	plans := make([]ReplicationPlan, 0, limit)
	for rows.Next() {
		var plan ReplicationPlan
		if err := scanReplicationPlan(rows, &plan); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func (s *PostgresStore) ListReplicationPlans(ctx context.Context, repositoryID string, limit int) ([]ReplicationPlan, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,source_repository_id::text,target_repository_id::text,format,idempotency_key,state,created_at,started_at,completed_at,last_error,next_attempt_at,lease_expires_at,lease_token,attempts,max_attempts FROM replication_plans WHERE source_repository_id::text=$1 OR target_repository_id::text=$1 ORDER BY created_at DESC LIMIT $2`, repositoryID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	plans := make([]ReplicationPlan, 0, limit)
	for rows.Next() {
		var plan ReplicationPlan
		if err := scanReplicationPlan(rows, &plan); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}

func (s *PostgresStore) GetReplicationPlan(ctx context.Context, repositoryID, id string) (ReplicationPlan, error) {
	var plan ReplicationPlan
	err := scanReplicationPlan(s.db.QueryRowContext(ctx, `SELECT id::text,source_repository_id::text,target_repository_id::text,format,idempotency_key,state,created_at,started_at,completed_at,last_error,next_attempt_at,lease_expires_at,lease_token,attempts,max_attempts FROM replication_plans WHERE id::text=$1 AND (source_repository_id::text=$2 OR target_repository_id::text=$2)`, id, repositoryID), &plan)
	if errors.Is(err, sql.ErrNoRows) {
		return ReplicationPlan{}, ErrNotFound
	}
	return plan, err
}

func (s *PostgresStore) ListReplicationCheckpoints(ctx context.Context, planID string) ([]ReplicationCheckpoint, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM replication_plans WHERE id::text=$1)`, planID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	checks, err := listReplicationCheckpoints(ctx, s.db, planID)
	return checks, err
}

func (s *PostgresStore) UpdateReplicationCheckpointWithLease(ctx context.Context, checkpoint ReplicationCheckpoint, leaseToken string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE replication_checkpoints c
		SET byte_offset=$4,state=$5,attempts=$6,last_error=$7,verified_at=$8,updated_at=now()
		FROM replication_plans p
		WHERE c.plan_id=p.id AND c.plan_id::text=$1 AND c.object_key=$2 AND p.state='running' AND p.lease_token=$3 AND p.lease_expires_at>now()`,
		checkpoint.PlanID, checkpoint.ObjectKey, leaseToken, checkpoint.ByteOffset, checkpoint.State, checkpoint.Attempts, checkpoint.LastError, nullableReplicationTime(checkpoint.VerifiedAt))
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE replication_plans SET lease_expires_at=now()+interval '10 minutes' WHERE id::text=$1 AND lease_token=$2`, checkpoint.PlanID, leaseToken); err != nil {
		return err
	}
	return nil
}

func (s *PostgresStore) CompleteReplicationPlanWithLease(ctx context.Context, id, leaseToken string) error {
	return s.finishReplicationPlan(ctx, id, "completed", "", leaseToken)
}

func (s *PostgresStore) FailReplicationPlanWithLease(ctx context.Context, id, message, leaseToken string) error {
	return s.finishReplicationPlan(ctx, id, "failed", message, leaseToken)
}

func (s *PostgresStore) finishReplicationPlan(ctx context.Context, id, state, message, leaseToken string) error {
	if state == "failed" {
		result, err := s.db.ExecContext(ctx, `UPDATE replication_plans SET state='failed',completed_at=now(),last_error=$3,lease_expires_at=NULL,lease_token=NULL,next_attempt_at=CASE WHEN attempts < max_attempts THEN now() ELSE NULL END WHERE id::text=$1 AND state='running' AND lease_token=$2 AND lease_expires_at>now()`, id, leaseToken, message)
		if err != nil {
			return err
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return ErrNotFound
		}
		return nil
	}
	result, err := s.db.ExecContext(ctx, `UPDATE replication_plans SET state=$2,completed_at=now(),last_error=$3,lease_expires_at=NULL,lease_token=NULL,next_attempt_at=NULL WHERE id::text=$1 AND state='running' AND lease_token=$4 AND lease_expires_at>now()`, id, state, message, leaseToken)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) RecoverExpiredReplicationPlans(ctx context.Context, before time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE replication_plans SET state='failed',last_error='replication worker lease expired',lease_expires_at=NULL,lease_token=NULL,next_attempt_at=CASE WHEN attempts < max_attempts THEN $1 ELSE NULL END WHERE state='running' AND lease_expires_at IS NOT NULL AND lease_expires_at <= $1`, before)
	if err != nil {
		return 0, err
	}
	count, _ := result.RowsAffected()
	return int(count), nil
}

// CancelReplicationPlan stops a pending or failed plan from being claimed again.
// Running plans are not cancellable because the worker owns them mid-flight;
// the caller is expected to have checked the state, so any zero-row result
// (missing or not pending/failed) is reported as ErrNotFound.
func (s *PostgresStore) CancelReplicationPlan(ctx context.Context, repositoryID, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE replication_plans SET state='cancelled',completed_at=now(),last_error='' WHERE id::text=$1 AND (source_repository_id::text=$2 OR target_repository_id::text=$2) AND state IN ('pending','failed')`, id, repositoryID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

type replicationPlanScanner interface{ Scan(...any) error }

func scanReplicationPlan(scanner replicationPlanScanner, plan *ReplicationPlan) error {
	var startedAt, completedAt, nextAttemptAt, leaseExpiresAt sql.NullTime
	var leaseToken sql.NullString
	if err := scanner.Scan(&plan.ID, &plan.SourceRepositoryID, &plan.TargetRepositoryID, &plan.Format, &plan.IdempotencyKey, &plan.State, &plan.CreatedAt, &startedAt, &completedAt, &plan.LastError, &nextAttemptAt, &leaseExpiresAt, &leaseToken, &plan.Attempts, &plan.MaxAttempts); err != nil {
		return err
	}
	if startedAt.Valid {
		plan.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		plan.CompletedAt = completedAt.Time
	}
	if nextAttemptAt.Valid {
		plan.NextAttemptAt = nextAttemptAt.Time
	}
	if leaseExpiresAt.Valid {
		plan.LeaseExpiresAt = leaseExpiresAt.Time
	}
	if leaseToken.Valid {
		plan.LeaseToken = leaseToken.String
	}
	return nil
}

type replicationCheckpointScanner interface{ Scan(...any) error }

func scanReplicationCheckpoint(scanner replicationCheckpointScanner, checkpoint *ReplicationCheckpoint) error {
	var verifiedAt sql.NullTime
	if err := scanner.Scan(&checkpoint.PlanID, &checkpoint.SourceObjectKey, &checkpoint.ObjectKey, &checkpoint.Digest, &checkpoint.Size, &checkpoint.ByteOffset, &checkpoint.State, &checkpoint.Attempts, &checkpoint.LastError, &verifiedAt, &checkpoint.UpdatedAt); err != nil {
		return err
	}
	if verifiedAt.Valid {
		checkpoint.VerifiedAt = verifiedAt.Time
	}
	return nil
}

type replicationCheckpointQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func listReplicationCheckpoints(ctx context.Context, query replicationCheckpointQuery, planID string) ([]ReplicationCheckpoint, error) {
	rows, err := query.QueryContext(ctx, `SELECT plan_id::text,source_object_key,object_key,digest,size,byte_offset,state,attempts,last_error,verified_at,updated_at FROM replication_checkpoints WHERE plan_id::text=$1 ORDER BY object_key`, planID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	checks := []ReplicationCheckpoint{}
	for rows.Next() {
		var checkpoint ReplicationCheckpoint
		if err := scanReplicationCheckpoint(rows, &checkpoint); err != nil {
			return nil, err
		}
		checks = append(checks, checkpoint)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return checks, nil
}

func nullableReplicationTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}

func equivalentReplicationCheckpoints(existing, requested []ReplicationCheckpoint) bool {
	if len(existing) != len(requested) {
		return false
	}
	want := append([]ReplicationCheckpoint(nil), requested...)
	sort.Slice(want, func(i, j int) bool { return want[i].ObjectKey < want[j].ObjectKey })
	for i, got := range existing {
		gotSource := got.SourceObjectKey
		if gotSource == "" {
			gotSource = got.ObjectKey
		}
		wantSource := want[i].SourceObjectKey
		if wantSource == "" {
			wantSource = want[i].ObjectKey
		}
		if gotSource != wantSource || got.ObjectKey != want[i].ObjectKey || got.Digest != want[i].Digest || got.Size != want[i].Size {
			return false
		}
	}
	return true
}
