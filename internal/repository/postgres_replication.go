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
		INSERT INTO replication_plans (id, source_repository_id, target_repository_id, format, idempotency_key, state)
		VALUES ($1,$2,$3,$4,$5,'pending')
		ON CONFLICT (target_repository_id, idempotency_key) DO NOTHING
		RETURNING id::text,source_repository_id::text,target_repository_id::text,format,idempotency_key,state,created_at,started_at,completed_at,last_error`,
		plan.ID, plan.SourceRepositoryID, plan.TargetRepositoryID, plan.Format, plan.IdempotencyKey), &plan)
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
	if err = scanReplicationPlan(tx.QueryRowContext(ctx, `SELECT id::text,source_repository_id::text,target_repository_id::text,format,idempotency_key,state,created_at,started_at,completed_at,last_error FROM replication_plans WHERE target_repository_id::text=$1 AND idempotency_key=$2 FOR UPDATE`, plan.TargetRepositoryID, plan.IdempotencyKey), &existing); err != nil {
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
	rows, err := s.db.QueryContext(ctx, `
		WITH candidates AS (
			SELECT id FROM replication_plans WHERE state IN ('pending','failed') AND ($1='' OR format=$1) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $2
		)
		UPDATE replication_plans p SET state='running',started_at=now(),completed_at=NULL,last_error=''
		FROM candidates WHERE p.id=candidates.id
		RETURNING p.id::text,p.source_repository_id::text,p.target_repository_id::text,p.format,p.idempotency_key,p.state,p.created_at,p.started_at,p.completed_at,p.last_error`, format, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,source_repository_id::text,target_repository_id::text,format,idempotency_key,state,created_at,started_at,completed_at,last_error FROM replication_plans WHERE source_repository_id::text=$1 OR target_repository_id::text=$1 ORDER BY created_at DESC LIMIT $2`, repositoryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
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
	err := scanReplicationPlan(s.db.QueryRowContext(ctx, `SELECT id::text,source_repository_id::text,target_repository_id::text,format,idempotency_key,state,created_at,started_at,completed_at,last_error FROM replication_plans WHERE id::text=$1 AND (source_repository_id::text=$2 OR target_repository_id::text=$2)`, id, repositoryID), &plan)
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

func (s *PostgresStore) UpdateReplicationCheckpoint(ctx context.Context, checkpoint ReplicationCheckpoint) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE replication_checkpoints c
		SET byte_offset=$3,state=$4,attempts=$5,last_error=$6,verified_at=$7,updated_at=now()
		FROM replication_plans p
		WHERE c.plan_id=p.id AND c.plan_id::text=$1 AND c.object_key=$2 AND p.state='running'`,
		checkpoint.PlanID, checkpoint.ObjectKey, checkpoint.ByteOffset, checkpoint.State, checkpoint.Attempts, checkpoint.LastError, nullableReplicationTime(checkpoint.VerifiedAt))
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) CompleteReplicationPlan(ctx context.Context, id string) error {
	return s.finishReplicationPlan(ctx, id, "completed", "")
}

func (s *PostgresStore) FailReplicationPlan(ctx context.Context, id, message string) error {
	return s.finishReplicationPlan(ctx, id, "failed", message)
}

func (s *PostgresStore) finishReplicationPlan(ctx context.Context, id, state, message string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE replication_plans SET state=$2,completed_at=now(),last_error=$3 WHERE id::text=$1 AND state='running'`, id, state, message)
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
	var startedAt, completedAt sql.NullTime
	if err := scanner.Scan(&plan.ID, &plan.SourceRepositoryID, &plan.TargetRepositoryID, &plan.Format, &plan.IdempotencyKey, &plan.State, &plan.CreatedAt, &startedAt, &completedAt, &plan.LastError); err != nil {
		return err
	}
	if startedAt.Valid {
		plan.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		plan.CompletedAt = completedAt.Time
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
	defer rows.Close()
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
