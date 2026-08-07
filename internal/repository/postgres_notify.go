package repository

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
)

var postgresNotificationChannels = []string{
	"artifact_gateway_audit_cleanup",
	"artifact_gateway_lifecycle_jobs",
	"artifact_gateway_replication_plans",
	"artifact_gateway_repository_deletions",
}

type postgresNotifier struct {
	db     *sql.DB
	ownsDB bool
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	wg     sync.WaitGroup

	mu          sync.Mutex
	closed      bool
	nextID      uint64
	subscribers map[string]map[uint64]chan struct{}
}

func newPostgresNotifier(db *sql.DB, ownsDB bool) *postgresNotifier {
	ctx, cancel := context.WithCancel(context.Background())
	return &postgresNotifier{db: db, ownsDB: ownsDB, ctx: ctx, cancel: cancel, subscribers: make(map[string]map[uint64]chan struct{})}
}

// Listen returns a reconnecting wake-up channel for a PostgreSQL NOTIFY
// channel. Notifications are hints only; callers must re-check their durable
// task table before doing work.
func (s *PostgresStore) Listen(ctx context.Context, channel string) <-chan struct{} {
	if s.notifier == nil {
		wake := make(chan struct{})
		close(wake)
		return wake
	}
	return s.notifier.Subscribe(ctx, channel)
}

func (n *postgresNotifier) Subscribe(ctx context.Context, channel string) <-chan struct{} {
	wake := make(chan struct{}, 1)
	if !knownPostgresNotificationChannel(channel) {
		close(wake)
		return wake
	}
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		close(wake)
		return wake
	}
	id := n.nextID
	n.nextID++
	if n.subscribers[channel] == nil {
		n.subscribers[channel] = make(map[uint64]chan struct{})
	}
	n.subscribers[channel][id] = wake
	n.once.Do(func() {
		n.wg.Add(1)
		go n.run()
	})
	n.wg.Add(1)
	n.mu.Unlock()
	go func() {
		defer n.wg.Done()
		select {
		case <-ctx.Done():
		case <-n.ctx.Done():
		}
		n.mu.Lock()
		if subscribers := n.subscribers[channel]; subscribers != nil {
			if subscriber, exists := subscribers[id]; exists {
				delete(subscribers, id)
				close(subscriber)
			}
			if len(subscribers) == 0 {
				delete(n.subscribers, channel)
			}
		}
		n.mu.Unlock()
	}()
	return wake
}

func (n *postgresNotifier) run() {
	defer n.wg.Done()
	for n.ctx.Err() == nil {
		conn, err := n.db.Conn(n.ctx)
		if err == nil {
			err = database.ListenChannels(n.ctx, conn, postgresNotificationChannels...)
		}
		if err != nil {
			if conn != nil {
				_ = conn.Close()
			}
			if !waitForPostgresNotificationRetry(n.ctx) {
				return
			}
			continue
		}
		for n.ctx.Err() == nil {
			channel, waitErr := database.WaitForNotification(n.ctx, conn)
			if waitErr != nil {
				break
			}
			n.dispatch(channel)
		}
		_ = conn.Close()
	}
}

func (n *postgresNotifier) dispatch(channel string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, subscriber := range n.subscribers[channel] {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (n *postgresNotifier) Close() error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil
	}
	n.closed = true
	n.cancel()
	n.mu.Unlock()
	n.wg.Wait()
	if n.ownsDB {
		return n.db.Close()
	}
	return nil
}

func knownPostgresNotificationChannel(channel string) bool {
	for _, known := range postgresNotificationChannels {
		if channel == known {
			return true
		}
	}
	return false
}

func waitForPostgresNotificationRetry(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
