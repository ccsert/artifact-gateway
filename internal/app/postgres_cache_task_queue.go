package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
	"github.com/google/uuid"
)

const cacheCollectionTask = "cache_collect"

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
	if _, err := q.db.ExecContext(ctx, `INSERT INTO cache_tasks (id, task_type, dedupe_key) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, uuid.NewString(), cacheCollectionTask, cacheCollectionTask); err != nil {
		return err
	}
	_, err := q.db.ExecContext(ctx, `SELECT pg_notify('artifact_gateway_cache_tasks', $1)`, cacheCollectionTask)
	return err
}

func (q *PostgresCacheTaskQueue) claimCollection(ctx context.Context, lease time.Duration) (postgresCacheTask, bool, error) {
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
RETURNING task.id::text, task.claim_token::text`, cacheCollectionTask, lease.String(), token).Scan(&task.ID, &task.ClaimToken)
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

func (q *PostgresCacheTaskQueue) StartCacheCollection(ctx context.Context, interval time.Duration, collect func(context.Context) error) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = q.EnqueueCollection(ctx)
		wake := q.listenForTasks(ctx)
		enqueue := time.NewTicker(interval)
		// LISTEN/NOTIFY wakes healthy workers immediately. This low-frequency
		// poll recovers queued work if a listener connection is interrupted.
		poll := time.NewTicker(5 * time.Second)
		defer enqueue.Stop()
		defer poll.Stop()
		for {
			task, claimed, err := q.claimCollection(ctx, interval*2)
			if err == nil && claimed {
				if err := collect(ctx); err != nil {
					_ = q.retry(ctx, task, time.Second)
				} else {
					_ = q.complete(ctx, task)
				}
				continue
			}
			select {
			case <-ctx.Done():
				return
			case <-enqueue.C:
				_ = q.EnqueueCollection(ctx)
			case <-wake:
			case <-poll.C:
			}
		}
	}()
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
