package repository

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) EnqueueLifecycleJob(ctx context.Context, job LifecycleJob) (LifecycleJob, bool, error) {
	err := scanLifecycleJob(s.db.QueryRowContext(ctx, `INSERT INTO lifecycle_jobs (id,repository_id,kind,idempotency_key,state,payload) VALUES ($1,$2,$3,$4,'pending',$5) ON CONFLICT (repository_id,kind,idempotency_key) DO NOTHING RETURNING id::text,repository_id::text,kind,idempotency_key,payload,state,created_at,started_at,completed_at,last_error`, job.ID, job.RepositoryID, job.Kind, job.IdempotencyKey, job.Payload), &job)
	if err == nil {
		return job, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return LifecycleJob{}, false, err
	}
	var existing LifecycleJob
	err = scanLifecycleJob(s.db.QueryRowContext(ctx, `SELECT id::text,repository_id::text,kind,idempotency_key,payload,state,created_at,started_at,completed_at,last_error FROM lifecycle_jobs WHERE repository_id::text=$1 AND kind=$2 AND idempotency_key=$3`, job.RepositoryID, job.Kind, job.IdempotencyKey), &existing)
	if err != nil {
		return LifecycleJob{}, false, err
	}
	if !equivalentLifecyclePayload(existing.Payload, job.Payload) {
		return LifecycleJob{}, false, ErrIdempotencyConflict
	}
	return existing, true, nil
}

func (s *PostgresStore) ClaimLifecycleJobs(ctx context.Context, limit int) ([]LifecycleJob, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (SELECT id FROM lifecycle_jobs WHERE state='pending' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $1) UPDATE lifecycle_jobs jobs SET state='running',started_at=now() FROM candidates WHERE jobs.id=candidates.id RETURNING jobs.id::text,jobs.repository_id::text,jobs.kind,jobs.idempotency_key,jobs.payload,jobs.state,jobs.created_at,jobs.started_at,jobs.completed_at,jobs.last_error`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []LifecycleJob
	for rows.Next() {
		var job LifecycleJob
		if err := scanLifecycleJob(rows, &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *PostgresStore) CompleteLifecycleJob(ctx context.Context, id string) error {
	return s.finishLifecycleJob(ctx, id, LifecycleJobCompleted, "")
}

func (s *PostgresStore) FailLifecycleJob(ctx context.Context, id, message string) error {
	return s.finishLifecycleJob(ctx, id, LifecycleJobFailed, message)
}

func (s *PostgresStore) finishLifecycleJob(ctx context.Context, id string, state LifecycleJobState, message string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE lifecycle_jobs SET state=$2,completed_at=now(),last_error=$3 WHERE id::text=$1 AND state='running'`, id, state, message)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

type lifecycleJobScanner interface {
	Scan(...any) error
}

func scanLifecycleJob(scanner lifecycleJobScanner, job *LifecycleJob) error {
	var startedAt, completedAt sql.NullTime
	if err := scanner.Scan(&job.ID, &job.RepositoryID, &job.Kind, &job.IdempotencyKey, &job.Payload, &job.State, &job.CreatedAt, &startedAt, &completedAt, &job.LastError); err != nil {
		return err
	}
	if startedAt.Valid {
		job.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = completedAt.Time
	}
	return nil
}
