package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const lifecycleJobColumns = `id::text,repository_id::text,kind,idempotency_key,payload,state,created_at,started_at,completed_at,last_error,attempts,max_attempts,next_attempt_at,lease_expires_at,lease_token,progress_current,progress_total,progress_message`

func (s *PostgresStore) EnqueueLifecycleJob(ctx context.Context, job LifecycleJob) (LifecycleJob, bool, error) {
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = DefaultLifecycleJobMaxAttempts
	}
	if job.ProgressTotal <= 0 && job.Kind != LifecycleJobRetention {
		job.ProgressTotal = 1
	}
	err := scanLifecycleJob(s.db.QueryRowContext(ctx, `INSERT INTO lifecycle_jobs (id,repository_id,kind,idempotency_key,state,payload,max_attempts,next_attempt_at,progress_total) VALUES ($1,$2,$3,$4,'pending',$5,$6,now(),$7) ON CONFLICT (repository_id,kind,idempotency_key) DO NOTHING RETURNING `+lifecycleJobColumns, job.ID, job.RepositoryID, job.Kind, job.IdempotencyKey, job.Payload, job.MaxAttempts, job.ProgressTotal), &job)
	if err == nil {
		return job, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return LifecycleJob{}, false, err
	}
	var existing LifecycleJob
	err = scanLifecycleJob(s.db.QueryRowContext(ctx, `SELECT `+lifecycleJobColumns+` FROM lifecycle_jobs WHERE repository_id::text=$1 AND kind=$2 AND idempotency_key=$3`, job.RepositoryID, job.Kind, job.IdempotencyKey), &existing)
	if err != nil {
		return LifecycleJob{}, false, err
	}
	if !equivalentLifecyclePayload(existing.Payload, job.Payload) {
		return LifecycleJob{}, false, ErrIdempotencyConflict
	}
	return existing, true, nil
}

func (s *PostgresStore) ListLifecycleJobs(ctx context.Context, repositoryID string, limit int) ([]LifecycleJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+lifecycleJobColumns+` FROM lifecycle_jobs WHERE repository_id::text=$1 ORDER BY created_at DESC LIMIT $2`, repositoryID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]LifecycleJob, 0, limit)
	for rows.Next() {
		var job LifecycleJob
		if err := scanLifecycleJob(rows, &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *PostgresStore) ListAllLifecycleJobs(ctx context.Context, limit int) ([]RepositoryLifecycleJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT h.name,`+prefixedLifecycleJobColumns("jobs")+`
		FROM lifecycle_jobs jobs
		JOIN hosted_repositories h ON h.id=jobs.repository_id
		ORDER BY jobs.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	records := make([]RepositoryLifecycleJob, 0, limit)
	for rows.Next() {
		var record RepositoryLifecycleJob
		if err := scanRepositoryLifecycleJob(rows, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *PostgresStore) GetLifecycleJob(ctx context.Context, repositoryID, id string) (LifecycleJob, error) {
	var job LifecycleJob
	err := scanLifecycleJob(s.db.QueryRowContext(ctx, `SELECT `+lifecycleJobColumns+` FROM lifecycle_jobs WHERE repository_id::text=$1 AND id::text=$2`, repositoryID, id), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return LifecycleJob{}, ErrNotFound
	}
	return job, err
}

func (s *PostgresStore) GetLatestArtifactScanJob(ctx context.Context, repositoryID string, format Format, coordinate, digest string) (LifecycleJob, error) {
	var job LifecycleJob
	err := scanLifecycleJob(s.db.QueryRowContext(ctx, `SELECT `+lifecycleJobColumns+`
		FROM lifecycle_jobs
		WHERE repository_id::text=$1 AND kind='scan'
		  AND payload->>'format'=$2 AND payload->>'coordinate'=$3 AND payload->>'digest'=$4
		ORDER BY created_at DESC,id DESC LIMIT 1`, repositoryID, format, coordinate, digest), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return LifecycleJob{}, ErrNotFound
	}
	return job, err
}

func (s *PostgresStore) ClaimLifecycleJobs(ctx context.Context, limit int) ([]LifecycleJob, error) {
	return s.claimLifecycleJobs(ctx, "", "", limit)
}

func (s *PostgresStore) ClaimLifecycleJobsByKind(ctx context.Context, kind LifecycleJobKind, limit int) ([]LifecycleJob, error) {
	return s.claimLifecycleJobs(ctx, kind, "", limit)
}

func (s *PostgresStore) ClaimLifecycleJobsByKindAndFormat(ctx context.Context, kind LifecycleJobKind, format Format, limit int) ([]LifecycleJob, error) {
	return s.claimLifecycleJobs(ctx, kind, format, limit)
}

func (s *PostgresStore) claimLifecycleJobs(ctx context.Context, kind LifecycleJobKind, format Format, limit int) ([]LifecycleJob, error) {
	if limit <= 0 {
		limit = 100
	}
	if _, err := s.RecoverExpiredLifecycleJobs(ctx, time.Now().UTC()); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `WITH per_repository AS (
        SELECT DISTINCT ON (jobs.repository_id) jobs.id,jobs.repository_id,jobs.next_attempt_at,jobs.created_at
        FROM lifecycle_jobs jobs
        WHERE jobs.state IN ('pending','retrying') AND COALESCE(jobs.next_attempt_at,jobs.created_at)<=now()
          AND jobs.attempts < jobs.max_attempts
          AND ($1='' OR jobs.kind=$1) AND ($2='' OR jobs.payload->>'format'=$2)
          AND NOT EXISTS (SELECT 1 FROM lifecycle_jobs running WHERE running.repository_id=jobs.repository_id AND running.state='running')
        ORDER BY jobs.repository_id,COALESCE(jobs.next_attempt_at,jobs.created_at),jobs.created_at
    ), candidates AS (
        SELECT id FROM per_repository
        WHERE pg_try_advisory_xact_lock(hashtextextended(repository_id::text, 0))
        ORDER BY COALESCE(next_attempt_at,created_at),created_at LIMIT $3
    ) UPDATE lifecycle_jobs jobs SET state='running',started_at=now(),completed_at=NULL,next_attempt_at=NULL,
        lease_expires_at=now()+interval '10 minutes',lease_token=gen_random_uuid()::text,attempts=jobs.attempts+1
    FROM candidates WHERE jobs.id=candidates.id AND jobs.state IN ('pending','retrying')
    RETURNING `+prefixedLifecycleJobColumns("jobs"), kind, format, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

func prefixedLifecycleJobColumns(prefix string) string {
	return prefix + `.id::text,` + prefix + `.repository_id::text,` + prefix + `.kind,` + prefix + `.idempotency_key,` + prefix + `.payload,` + prefix + `.state,` + prefix + `.created_at,` + prefix + `.started_at,` + prefix + `.completed_at,` + prefix + `.last_error,` + prefix + `.attempts,` + prefix + `.max_attempts,` + prefix + `.next_attempt_at,` + prefix + `.lease_expires_at,` + prefix + `.lease_token,` + prefix + `.progress_current,` + prefix + `.progress_total,` + prefix + `.progress_message`
}

func (s *PostgresStore) RecoverExpiredLifecycleJobs(ctx context.Context, before time.Time) (int, error) {
	result, err := s.db.ExecContext(ctx, `WITH candidates AS (
		SELECT id FROM lifecycle_jobs
		WHERE state='running' AND lease_expires_at IS NOT NULL AND lease_expires_at<=$1
		AND pg_try_advisory_xact_lock(hashtextextended('lifecycle-job-lease:' || id::text, 0))
		FOR UPDATE SKIP LOCKED
	) UPDATE lifecycle_jobs jobs SET
        state=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'retrying' END,
        completed_at=CASE WHEN attempts>=max_attempts THEN $1 ELSE NULL END,
        next_attempt_at=CASE WHEN attempts>=max_attempts THEN NULL ELSE $1+LEAST(interval '30 minutes',interval '30 seconds'*power(2,LEAST(GREATEST(attempts-1,0),6))) END,
        lease_expires_at=NULL,lease_token='',last_error='worker lease expired before completion'
		FROM candidates WHERE jobs.id=candidates.id`, before.UTC())
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

func (s *PostgresStore) RunLifecycleJobNow(ctx context.Context, repositoryID, id string) (LifecycleJob, error) {
	return s.controlLifecycleJob(ctx, repositoryID, id, `UPDATE lifecycle_jobs SET state='pending',next_attempt_at=now() WHERE repository_id::text=$1 AND id::text=$2 AND state IN ('pending','retrying') RETURNING `+lifecycleJobColumns)
}

func (s *PostgresStore) RetryLifecycleJob(ctx context.Context, repositoryID, id string) (LifecycleJob, error) {
	return s.controlLifecycleJob(ctx, repositoryID, id, `UPDATE lifecycle_jobs SET state='pending',attempts=0,next_attempt_at=now(),started_at=NULL,completed_at=NULL,lease_expires_at=NULL,last_error='',progress_current=0,progress_message='' WHERE repository_id::text=$1 AND id::text=$2 AND state IN ('failed','cancelled') RETURNING `+lifecycleJobColumns)
}

func (s *PostgresStore) RequeueFailedLifecycleJobs(ctx context.Context, repositoryID string, kind LifecycleJobKind, limit int) ([]LifecycleJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
        SELECT id FROM lifecycle_jobs
        WHERE repository_id::text=$1 AND kind=$2 AND state IN ('failed','cancelled')
        ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT $3
    ) UPDATE lifecycle_jobs jobs SET state='pending',attempts=0,next_attempt_at=now(),started_at=NULL,
        completed_at=NULL,lease_expires_at=NULL,lease_token='',last_error='',progress_current=0,progress_message=''
    FROM candidates WHERE jobs.id=candidates.id RETURNING `+prefixedLifecycleJobColumns("jobs"), repositoryID, kind, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]LifecycleJob, 0, limit)
	for rows.Next() {
		var job LifecycleJob
		if err := scanLifecycleJob(rows, &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *PostgresStore) CancelLifecycleJob(ctx context.Context, repositoryID, id string) (LifecycleJob, error) {
	return s.controlLifecycleJob(ctx, repositoryID, id, `UPDATE lifecycle_jobs SET state='cancelled',completed_at=now(),next_attempt_at=NULL WHERE repository_id::text=$1 AND id::text=$2 AND state IN ('pending','retrying') RETURNING `+lifecycleJobColumns)
}

func (s *PostgresStore) controlLifecycleJob(ctx context.Context, repositoryID, id, query string) (LifecycleJob, error) {
	var job LifecycleJob
	err := scanLifecycleJob(s.db.QueryRowContext(ctx, query, repositoryID, id), &job)
	if err == nil {
		return job, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return LifecycleJob{}, err
	}
	if _, getErr := s.GetLifecycleJob(ctx, repositoryID, id); errors.Is(getErr, ErrNotFound) {
		return LifecycleJob{}, ErrNotFound
	} else if getErr != nil {
		return LifecycleJob{}, getErr
	}
	return LifecycleJob{}, ErrVersionConflict
}

func (s *PostgresStore) UpdateLifecycleJobProgress(ctx context.Context, id, leaseToken string, current, total int, message string) error {
	if current < 0 || total < 0 || current > total {
		return ErrVersionConflict
	}
	result, err := s.db.ExecContext(ctx, `UPDATE lifecycle_jobs SET progress_current=$3,progress_total=$4,progress_message=$5,
		lease_expires_at=GREATEST(lease_expires_at+interval '1 microsecond',clock_timestamp()+interval '10 minutes')
		WHERE id::text=$1 AND lease_token=$2 AND state='running'`, id, leaseToken, current, total, message)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) RenewLifecycleJobLease(ctx context.Context, id, leaseToken string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE lifecycle_jobs SET
		lease_expires_at=GREATEST(lease_expires_at+interval '1 microsecond',clock_timestamp()+interval '10 minutes')
		WHERE id::text=$1 AND lease_token=$2 AND state='running' AND lease_expires_at>clock_timestamp()`, id, leaseToken)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) LockLifecycleJobLease(ctx context.Context, id, leaseToken string) (func(), error) {
	_, release, err := s.lockPostgresAdvisoryKeys(ctx, []string{"lifecycle-job-lease:" + id})
	if err != nil {
		return nil, err
	}
	var valid bool
	if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM lifecycle_jobs WHERE id::text=$1 AND state='running' AND lease_token=$2 AND lease_expires_at>clock_timestamp())`, id, leaseToken).Scan(&valid); err != nil {
		release()
		return nil, err
	}
	if !valid {
		release()
		return nil, ErrNotFound
	}
	return release, nil
}

func (s *PostgresStore) CompleteLifecycleJob(ctx context.Context, id, leaseToken string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE lifecycle_jobs SET state='completed',completed_at=now(),lease_expires_at=NULL,lease_token='',next_attempt_at=NULL,last_error='',progress_current=CASE WHEN progress_total>0 THEN progress_total ELSE progress_current END WHERE id::text=$1 AND lease_token=$2 AND state='running' AND lease_expires_at>clock_timestamp()`, id, leaseToken)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) FailLifecycleJob(ctx context.Context, id, leaseToken, message string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE lifecycle_jobs SET
        state=CASE WHEN attempts>=max_attempts THEN 'failed' ELSE 'retrying' END,
        completed_at=CASE WHEN attempts>=max_attempts THEN now() ELSE NULL END,
        next_attempt_at=CASE WHEN attempts>=max_attempts THEN NULL ELSE now()+LEAST(interval '30 minutes',interval '30 seconds'*power(2,LEAST(GREATEST(attempts-1,0),6))) END,
        lease_expires_at=NULL,lease_token='',last_error=$3
        WHERE id::text=$1 AND lease_token=$2 AND state='running'`, id, leaseToken, message)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrNotFound
	}
	return nil
}

type lifecycleJobScanner interface {
	Scan(...any) error
}

func scanLifecycleJob(scanner lifecycleJobScanner, job *LifecycleJob) error {
	var startedAt, completedAt, nextAttemptAt, leaseExpiresAt sql.NullTime
	if err := scanner.Scan(&job.ID, &job.RepositoryID, &job.Kind, &job.IdempotencyKey, &job.Payload, &job.State, &job.CreatedAt, &startedAt, &completedAt, &job.LastError, &job.Attempts, &job.MaxAttempts, &nextAttemptAt, &leaseExpiresAt, &job.LeaseToken, &job.ProgressCurrent, &job.ProgressTotal, &job.ProgressMessage); err != nil {
		return err
	}
	if startedAt.Valid {
		job.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = completedAt.Time
	}
	if nextAttemptAt.Valid {
		job.NextAttemptAt = nextAttemptAt.Time
	}
	if leaseExpiresAt.Valid {
		job.LeaseExpiresAt = leaseExpiresAt.Time
	}
	return nil
}

func scanRepositoryLifecycleJob(scanner lifecycleJobScanner, record *RepositoryLifecycleJob) error {
	job := &record.Job
	var startedAt, completedAt, nextAttemptAt, leaseExpiresAt sql.NullTime
	if err := scanner.Scan(&record.RepositoryName, &job.ID, &job.RepositoryID, &job.Kind, &job.IdempotencyKey, &job.Payload, &job.State, &job.CreatedAt, &startedAt, &completedAt, &job.LastError, &job.Attempts, &job.MaxAttempts, &nextAttemptAt, &leaseExpiresAt, &job.LeaseToken, &job.ProgressCurrent, &job.ProgressTotal, &job.ProgressMessage); err != nil {
		return err
	}
	if startedAt.Valid {
		job.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = completedAt.Time
	}
	if nextAttemptAt.Valid {
		job.NextAttemptAt = nextAttemptAt.Time
	}
	if leaseExpiresAt.Valid {
		job.LeaseExpiresAt = leaseExpiresAt.Time
	}
	return nil
}
