package repository

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"
)

const scheduledTaskColumns = `id::text,name,description,kind,COALESCE(repository_id::text,''),interval_seconds,enabled,next_run_at,last_run_at,COALESCE(last_run_id::text,''),last_run_state,last_error,version,created_at,updated_at`
const scheduledTaskRunColumns = `id::text,task_id::text,trigger,state,scheduled_at,created_at,completed_at,target_kind,target_id,last_error`

func (s *PostgresStore) CreateScheduledTask(ctx context.Context, task ScheduledTask) (ScheduledTask, error) {
	if task.Version == "" {
		task.Version = "1"
	}
	err := scanScheduledTask(s.db.QueryRowContext(ctx, `INSERT INTO scheduled_tasks (id,name,description,kind,repository_id,interval_seconds,enabled,next_run_at) VALUES ($1,$2,$3,$4,NULLIF($5,'')::uuid,$6,$7,$8) RETURNING `+scheduledTaskColumns, task.ID, task.Name, task.Description, task.Kind, task.RepositoryID, task.IntervalSeconds, task.Enabled, task.NextRunAt), &task)
	if isUnique(err) {
		return ScheduledTask{}, ErrNameExists
	}
	return task, err
}

func (s *PostgresStore) ListScheduledTasks(ctx context.Context) ([]ScheduledTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduledTaskColumns+` FROM scheduled_tasks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]ScheduledTask, 0)
	for rows.Next() {
		var task ScheduledTask
		if err = scanScheduledTask(rows, &task); err != nil {
			return nil, err
		}
		items = append(items, task)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetScheduledTask(ctx context.Context, id string) (ScheduledTask, error) {
	var task ScheduledTask
	err := scanScheduledTask(s.db.QueryRowContext(ctx, `SELECT `+scheduledTaskColumns+` FROM scheduled_tasks WHERE id::text=$1`, id), &task)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduledTask{}, ErrNotFound
	}
	return task, err
}

func (s *PostgresStore) UpdateScheduledTask(ctx context.Context, task ScheduledTask, expectedVersion string) (ScheduledTask, error) {
	version, err := strconv.ParseInt(expectedVersion, 10, 64)
	if err != nil || version < 1 {
		return ScheduledTask{}, ErrVersionConflict
	}
	err = scanScheduledTask(s.db.QueryRowContext(ctx, `UPDATE scheduled_tasks SET name=$1,description=$2,kind=$3,repository_id=NULLIF($4,'')::uuid,interval_seconds=$5,enabled=$6,next_run_at=$7,version=version+1,updated_at=now() WHERE id::text=$8 AND version=$9 RETURNING `+scheduledTaskColumns, task.Name, task.Description, task.Kind, task.RepositoryID, task.IntervalSeconds, task.Enabled, task.NextRunAt, task.ID, version), &task)
	if errors.Is(err, sql.ErrNoRows) {
		var exists bool
		if checkErr := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM scheduled_tasks WHERE id::text=$1)`, task.ID).Scan(&exists); checkErr != nil {
			return ScheduledTask{}, checkErr
		}
		if !exists {
			return ScheduledTask{}, ErrNotFound
		}
		return ScheduledTask{}, ErrVersionConflict
	}
	if isUnique(err) {
		return ScheduledTask{}, ErrNameExists
	}
	return task, err
}

func (s *PostgresStore) DeleteScheduledTask(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_tasks WHERE id::text=$1`, id)
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

func (s *PostgresStore) ClaimDueScheduledTasks(ctx context.Context, now time.Time, limit int) ([]ScheduledTaskClaim, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `WITH candidates AS (
		SELECT id,gen_random_uuid() AS run_id FROM scheduled_tasks WHERE enabled AND next_run_at<=$1 ORDER BY next_run_at FOR UPDATE SKIP LOCKED LIMIT $2
	), updated AS (
		UPDATE scheduled_tasks tasks SET last_run_at=$1,last_run_id=candidates.run_id,last_run_state='failed',last_error='dispatch interrupted before submission',next_run_at=$1+make_interval(secs=>tasks.interval_seconds),updated_at=$1
		FROM candidates WHERE tasks.id=candidates.id RETURNING tasks.*
	), runs AS (
		INSERT INTO scheduled_task_runs (id,task_id,trigger,state,scheduled_at,created_at,last_error)
		SELECT candidates.run_id,updated.id,'scheduled','failed',$1,$1,'dispatch interrupted before submission' FROM updated JOIN candidates ON candidates.id=updated.id
		RETURNING *
	)
	SELECT `+prefixedScheduledTaskColumns("updated")+`,`+prefixedScheduledTaskRunColumns("runs")+` FROM updated JOIN runs ON runs.task_id=updated.id ORDER BY updated.next_run_at`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	claims := make([]ScheduledTaskClaim, 0)
	for rows.Next() {
		var claim ScheduledTaskClaim
		if err = scanScheduledTaskClaim(rows, &claim); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, rows.Err()
}

func (s *PostgresStore) CreateScheduledTaskRun(ctx context.Context, taskID, trigger string, now time.Time) (ScheduledTaskRun, error) {
	var run ScheduledTaskRun
	err := scanScheduledTaskRun(s.db.QueryRowContext(ctx, `WITH created AS (
		INSERT INTO scheduled_task_runs (id,task_id,trigger,state,scheduled_at,created_at,last_error)
		SELECT gen_random_uuid(),id,$2,'failed',$3,$3,'dispatch interrupted before submission' FROM scheduled_tasks WHERE id::text=$1 RETURNING *
	), touched AS (
		UPDATE scheduled_tasks tasks SET last_run_at=$3,last_run_id=created.id,last_run_state='failed',last_error='dispatch interrupted before submission',updated_at=$3 FROM created WHERE tasks.id=created.task_id
    ) SELECT `+scheduledTaskRunColumns+` FROM created`, taskID, trigger, now.UTC()), &run)
	if errors.Is(err, sql.ErrNoRows) {
		return ScheduledTaskRun{}, ErrNotFound
	}
	return run, err
}

func (s *PostgresStore) ListScheduledTaskRuns(ctx context.Context, taskID string, limit int) ([]ScheduledTaskRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduledTaskRunColumns+` FROM scheduled_task_runs WHERE task_id::text=$1 ORDER BY created_at DESC LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]ScheduledTaskRun, 0)
	for rows.Next() {
		var run ScheduledTaskRun
		if err = scanScheduledTaskRun(rows, &run); err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpdateScheduledTaskRun(ctx context.Context, run ScheduledTaskRun) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var taskID string
	err = tx.QueryRowContext(ctx, `UPDATE scheduled_task_runs SET state=$1,completed_at=$2,target_kind=$3,target_id=$4,last_error=$5 WHERE id::text=$6 RETURNING task_id::text`, run.State, run.CompletedAt, run.TargetKind, run.TargetID, run.LastError, run.ID).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE scheduled_tasks SET last_run_state=$1,last_error=$2,updated_at=now() WHERE id::text=$3`, run.State, run.LastError, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

type scheduledTaskScanner interface{ Scan(...any) error }

func scanScheduledTask(scanner scheduledTaskScanner, task *ScheduledTask) error {
	var lastRunAt sql.NullTime
	var version int64
	err := scanner.Scan(&task.ID, &task.Name, &task.Description, &task.Kind, &task.RepositoryID, &task.IntervalSeconds, &task.Enabled, &task.NextRunAt, &lastRunAt, &task.LastRunID, &task.LastRunState, &task.LastError, &version, &task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		return err
	}
	if lastRunAt.Valid {
		task.LastRunAt = lastRunAt.Time
	}
	task.Version = strconv.FormatInt(version, 10)
	return nil
}

func scanScheduledTaskRun(scanner scheduledTaskScanner, run *ScheduledTaskRun) error {
	var completedAt sql.NullTime
	err := scanner.Scan(&run.ID, &run.TaskID, &run.Trigger, &run.State, &run.ScheduledAt, &run.CreatedAt, &completedAt, &run.TargetKind, &run.TargetID, &run.LastError)
	if completedAt.Valid {
		run.CompletedAt = completedAt.Time
	}
	return err
}

func scanScheduledTaskClaim(scanner scheduledTaskScanner, claim *ScheduledTaskClaim) error {
	var lastRunAt, completedAt sql.NullTime
	var version int64
	err := scanner.Scan(&claim.Task.ID, &claim.Task.Name, &claim.Task.Description, &claim.Task.Kind, &claim.Task.RepositoryID, &claim.Task.IntervalSeconds, &claim.Task.Enabled, &claim.Task.NextRunAt, &lastRunAt, &claim.Task.LastRunID, &claim.Task.LastRunState, &claim.Task.LastError, &version, &claim.Task.CreatedAt, &claim.Task.UpdatedAt, &claim.Run.ID, &claim.Run.TaskID, &claim.Run.Trigger, &claim.Run.State, &claim.Run.ScheduledAt, &claim.Run.CreatedAt, &completedAt, &claim.Run.TargetKind, &claim.Run.TargetID, &claim.Run.LastError)
	if err != nil {
		return err
	}
	if lastRunAt.Valid {
		claim.Task.LastRunAt = lastRunAt.Time
	}
	if completedAt.Valid {
		claim.Run.CompletedAt = completedAt.Time
	}
	claim.Task.Version = strconv.FormatInt(version, 10)
	return nil
}

func prefixedScheduledTaskColumns(prefix string) string {
	return prefix + `.id::text,` + prefix + `.name,` + prefix + `.description,` + prefix + `.kind,COALESCE(` + prefix + `.repository_id::text,''),` + prefix + `.interval_seconds,` + prefix + `.enabled,` + prefix + `.next_run_at,` + prefix + `.last_run_at,COALESCE(` + prefix + `.last_run_id::text,''),` + prefix + `.last_run_state,` + prefix + `.last_error,` + prefix + `.version,` + prefix + `.created_at,` + prefix + `.updated_at`
}

func prefixedScheduledTaskRunColumns(prefix string) string {
	return prefix + `.id::text,` + prefix + `.task_id::text,` + prefix + `.trigger,` + prefix + `.state,` + prefix + `.scheduled_at,` + prefix + `.created_at,` + prefix + `.completed_at,` + prefix + `.target_kind,` + prefix + `.target_id,` + prefix + `.last_error`
}
