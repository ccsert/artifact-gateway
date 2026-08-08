package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
	"github.com/google/uuid"
)

const (
	legacyCacheCollectionTask  = "cache_collect"
	cacheCollectionTaskPrefix  = "cache_collect:"
	cacheCollectionFormatOCI   = "oci"
	cacheCollectionFormatRaw   = "raw"
	cacheCollectionFormatConan = "conan"
)

var cacheCollectionFormats = []string{cacheCollectionFormatOCI, cacheCollectionFormatRaw, cacheCollectionFormatConan}

type postgresCacheTask struct {
	ID, ClaimToken string
}

// PostgresCacheTaskQueue provides durable, multi-instance cache work. Tasks
// are claimed in short PostgreSQL transactions with SKIP LOCKED; an abandoned
// claim becomes eligible again after its lease period.
type PostgresCacheTaskQueue struct {
	db             *sql.DB
	listenerDB     *sql.DB
	ownsDB         bool
	ownsListenerDB bool
}

func NewPostgresCacheTaskQueue(databaseURL string) (*PostgresCacheTaskQueue, error) {
	db, err := database.OpenPostgres(databaseURL, database.DefaultPoolConfig())
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL cache task queue: %w", err)
	}
	listenerDB, err := database.OpenPostgres(databaseURL, database.NotificationPoolConfig())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open PostgreSQL cache task listener: %w", err)
	}
	return &PostgresCacheTaskQueue{db: db, listenerDB: listenerDB, ownsDB: true, ownsListenerDB: true}, nil
}

func NewPostgresCacheTaskQueueWithDB(db *sql.DB, databaseURL string) (*PostgresCacheTaskQueue, error) {
	if db == nil {
		return nil, fmt.Errorf("PostgreSQL cache task queue requires a database pool")
	}
	listenerDB, err := database.OpenPostgres(databaseURL, database.NotificationPoolConfig())
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL cache task listener: %w", err)
	}
	return &PostgresCacheTaskQueue{db: db, listenerDB: listenerDB, ownsListenerDB: true}, nil
}

func NewPostgresCacheTaskQueueWithPools(db, listenerDB *sql.DB) (*PostgresCacheTaskQueue, error) {
	if db == nil || listenerDB == nil {
		return nil, fmt.Errorf("PostgreSQL cache task queue requires database and listener pools")
	}
	return &PostgresCacheTaskQueue{db: db, listenerDB: listenerDB}, nil
}

func (q *PostgresCacheTaskQueue) Close() error {
	var listenerErr error
	if q.ownsListenerDB {
		listenerErr = q.listenerDB.Close()
	}
	if q.ownsDB {
		if err := q.db.Close(); err != nil {
			return err
		}
	}
	return listenerErr
}

func (q *PostgresCacheTaskQueue) ListenerDatabaseStats() sql.DBStats {
	return q.listenerDB.Stats()
}

func (q *PostgresCacheTaskQueue) EnqueueCollection(ctx context.Context) error {
	// Retire the pre-format queue entry during rolling upgrades. The format
	// tasks below replace it without losing a scheduled collection pass.
	if _, err := q.db.ExecContext(ctx, `UPDATE cache_tasks SET completed_at=now() WHERE task_type=$1 AND completed_at IS NULL`, legacyCacheCollectionTask); err != nil {
		return err
	}
	for _, format := range cacheCollectionFormats {
		if err := q.EnqueueCollectionForFormat(ctx, format); err != nil {
			return err
		}
	}
	return nil
}

func (q *PostgresCacheTaskQueue) EnqueueCollectionForFormat(ctx context.Context, format string) error {
	if !supportsCacheCollectionFormat(format) {
		return fmt.Errorf("unsupported cache collection format %q", format)
	}
	taskType := cacheCollectionTaskPrefix + format
	if _, err := q.db.ExecContext(ctx, `INSERT INTO cache_tasks (id, task_type, dedupe_key) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, uuid.NewString(), taskType, taskType); err != nil {
		return err
	}
	_, err := q.db.ExecContext(ctx, `SELECT pg_notify('artifact_gateway_cache_tasks', $1)`, format)
	return err
}

func (q *PostgresCacheTaskQueue) claimCollection(ctx context.Context, format string, lease time.Duration) (postgresCacheTask, bool, error) {
	if !supportsCacheCollectionFormat(format) {
		return postgresCacheTask{}, false, fmt.Errorf("unsupported cache collection format %q", format)
	}
	token := uuid.NewString()
	var task postgresCacheTask
	err := q.db.QueryRowContext(ctx, `WITH candidate AS (
    SELECT id FROM cache_tasks
    WHERE task_type=$1 AND completed_at IS NULL AND available_at <= now()
      AND (claimed_at IS NULL OR claimed_at < now() - $2::interval)
    ORDER BY available_at, created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE cache_tasks AS task
SET claimed_at=now(), claim_token=$3::uuid, attempts=task.attempts+1
FROM candidate
WHERE task.id=candidate.id
RETURNING task.id::text, task.claim_token::text`, cacheCollectionTaskPrefix+format, lease.String(), token).Scan(&task.ID, &task.ClaimToken)
	if err == sql.ErrNoRows {
		return postgresCacheTask{}, false, nil
	}
	if err != nil {
		return postgresCacheTask{}, false, err
	}
	return task, true, nil
}

func (q *PostgresCacheTaskQueue) complete(ctx context.Context, task postgresCacheTask) error {
	result, err := q.db.ExecContext(ctx, `UPDATE cache_tasks SET completed_at=now() WHERE id=$1::uuid AND claim_token=$2::uuid AND completed_at IS NULL`, task.ID, task.ClaimToken)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return err
		}
		return fmt.Errorf("cache task claim was lost")
	}
	return nil
}

func (q *PostgresCacheTaskQueue) retry(ctx context.Context, task postgresCacheTask, delay time.Duration) error {
	_, err := q.db.ExecContext(ctx, `UPDATE cache_tasks SET claimed_at=NULL, claim_token=NULL, available_at=now() + $3::interval WHERE id=$1::uuid AND claim_token=$2::uuid AND completed_at IS NULL`, task.ID, task.ClaimToken, delay.String())
	return err
}

func (q *PostgresCacheTaskQueue) StartCacheCollection(ctx context.Context, interval time.Duration, collect func(context.Context, string) error) {
	q.StartCacheScheduler(ctx, interval)
	q.StartCacheWorker(ctx, cacheCollectionFormats, collect)
}

// StartCacheScheduler periodically creates one durable, deduplicated cache
// collection task per supported format. It does not claim or execute work, so
// scheduler replicas can be separated from worker replicas.
func (q *PostgresCacheTaskQueue) StartCacheScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = q.EnqueueCollection(ctx)
		enqueue := time.NewTicker(interval)
		defer enqueue.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-enqueue.C:
				_ = q.EnqueueCollection(ctx)
			}
		}
	}()
}

// StartCacheWorker claims and executes durable collection tasks. A low
// frequency poll remains as a recovery path when LISTEN/NOTIFY is interrupted.
func (q *PostgresCacheTaskQueue) StartCacheWorker(ctx context.Context, formats []string, collect func(context.Context, string) error) {
	formats = supportedCacheCollectionFormats(formats)
	if collect == nil || len(formats) == 0 {
		return
	}
	go func() {
		wake := q.listenForTasks(ctx)
		poll := time.NewTicker(5 * time.Second)
		defer poll.Stop()
		for ctx.Err() == nil {
			processed := false
			for _, format := range formats {
				task, claimed, err := q.claimCollection(ctx, format, 10*time.Minute)
				if err == nil && claimed {
					if err := collect(ctx, format); err != nil {
						_ = q.retry(ctx, task, time.Second)
					} else {
						_ = q.complete(ctx, task)
					}
					processed = true
					break
				}
			}
			if processed {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-poll.C:
			}
		}
	}()
}

func supportsCacheCollectionFormat(format string) bool {
	for _, supported := range cacheCollectionFormats {
		if format == supported {
			return true
		}
	}
	return false
}

func supportedCacheCollectionFormats(formats []string) []string {
	result := make([]string, 0, len(cacheCollectionFormats))
	for _, supported := range cacheCollectionFormats {
		for _, format := range formats {
			if format == supported {
				result = append(result, supported)
				break
			}
		}
	}
	return result
}

func (q *PostgresCacheTaskQueue) listenForTasks(ctx context.Context) <-chan struct{} {
	wake := make(chan struct{}, 1)
	go func() {
		for ctx.Err() == nil {
			conn, err := q.listenerDB.Conn(ctx)
			if err == nil {
				err = database.ListenChannels(ctx, conn, "artifact_gateway_cache_tasks")
			}
			if err != nil {
				if conn != nil {
					_ = conn.Close()
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			for ctx.Err() == nil {
				if _, err := database.WaitForNotification(ctx, conn); err != nil {
					break
				}
				select {
				case wake <- struct{}{}:
				default:
				}
			}
			_ = conn.Close()
		}
	}()
	return wake
}
