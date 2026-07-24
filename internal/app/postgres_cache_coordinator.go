package app

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// PostgresCacheCoordinator uses session-scoped advisory locks for work that
// must be serialized across Gateway instances. A lost database connection
// releases its lock automatically, so lock ownership never outlives a process.
// Short-lived upstream circuit state is durable in PostgreSQL as well.
type PostgresCacheCoordinator struct {
	db     *sql.DB
	mu     sync.Mutex
	leases map[string]*sql.Conn
}

func NewPostgresCacheCoordinator(databaseURL string) (*PostgresCacheCoordinator, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL cache coordinator: %w", err)
	}
	return &PostgresCacheCoordinator{db: db, leases: make(map[string]*sql.Conn)}, nil
}

func (c *PostgresCacheCoordinator) Acquire(ctx context.Context, key string, _ time.Duration) (string, bool, error) {
	owner, err := newOCILockOwner()
	if err != nil {
		return "", false, err
	}
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return "", false, err
	}
	locked := false
	err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, "artifact-gateway:cache:"+key).Scan(&locked)
	if err != nil || !locked {
		_ = conn.Close()
		return owner, false, err
	}
	c.mu.Lock()
	c.leases[owner] = conn
	c.mu.Unlock()
	return owner, true, nil
}

func (c *PostgresCacheCoordinator) Renew(ctx context.Context, _ string, owner string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	conn := c.leases[owner]
	c.mu.Unlock()
	if conn == nil {
		return false, nil
	}
	if err := conn.PingContext(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (c *PostgresCacheCoordinator) Release(ctx context.Context, key, owner string) error {
	c.mu.Lock()
	conn := c.leases[owner]
	delete(c.leases, owner)
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	_, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "artifact-gateway:cache:"+key)
	closeErr := conn.Close()
	return joinCacheCoordinatorErrors(err, closeErr)
}

func (c *PostgresCacheCoordinator) CircuitOpen(ctx context.Context, key string) (bool, error) {
	var open bool
	err := c.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cache_circuit_breakers WHERE key=$1 AND expires_at > now())`, key).Scan(&open)
	return open, err
}

func (c *PostgresCacheCoordinator) OpenCircuit(ctx context.Context, key string, ttl time.Duration) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO cache_circuit_breakers (key, expires_at, updated_at) VALUES ($1, now() + $2::interval, now()) ON CONFLICT (key) DO UPDATE SET expires_at=EXCLUDED.expires_at, updated_at=EXCLUDED.updated_at`, key, ttl.String())
	return err
}

func (c *PostgresCacheCoordinator) CloseCircuit(ctx context.Context, key string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM cache_circuit_breakers WHERE key=$1`, key)
	return err
}

func (c *PostgresCacheCoordinator) Close() error {
	c.mu.Lock()
	leases := c.leases
	c.leases = make(map[string]*sql.Conn)
	c.mu.Unlock()
	for _, conn := range leases {
		_ = conn.Close()
	}
	return c.db.Close()
}

func joinCacheCoordinatorErrors(first, second error) error {
	if first != nil {
		return first
	}
	return second
}
