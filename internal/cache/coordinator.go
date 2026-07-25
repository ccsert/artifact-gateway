// Package cache owns cross-instance cache coordination.
package cache

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const DefaultLockLease = 35 * time.Second
const DefaultLockRenewInterval = DefaultLockLease / 3

// Coordinator serializes cache publication and persists short-lived upstream
// circuit state across Gateway instances.
type Coordinator interface {
	Acquire(context.Context, string, time.Duration) (string, bool, error)
	Renew(context.Context, string, string, time.Duration) (bool, error)
	Release(context.Context, string, string) error
	CircuitOpen(context.Context, string) (bool, error)
	OpenCircuit(context.Context, string, time.Duration) error
	CloseCircuit(context.Context, string) error
}

// PostgresCoordinator uses session-scoped advisory locks. A lost database
// connection releases its lock automatically, so ownership cannot outlive a
// Gateway process.
type PostgresCoordinator struct {
	db     *sql.DB
	mu     sync.Mutex
	leases map[string]*sql.Conn
}

func NewPostgresCoordinator(databaseURL string) (*PostgresCoordinator, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL cache coordinator: %w", err)
	}
	return &PostgresCoordinator{db: db, leases: make(map[string]*sql.Conn)}, nil
}

func (c *PostgresCoordinator) Acquire(ctx context.Context, key string, _ time.Duration) (string, bool, error) {
	owner, err := newOwner()
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

func (c *PostgresCoordinator) Renew(ctx context.Context, _ string, owner string, _ time.Duration) (bool, error) {
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

func (c *PostgresCoordinator) Release(ctx context.Context, key, owner string) error {
	c.mu.Lock()
	conn := c.leases[owner]
	delete(c.leases, owner)
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	_, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, "artifact-gateway:cache:"+key)
	closeErr := conn.Close()
	return joinErrors(err, closeErr)
}

func (c *PostgresCoordinator) CircuitOpen(ctx context.Context, key string) (bool, error) {
	var open bool
	err := c.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cache_circuit_breakers WHERE key=$1 AND expires_at > now())`, key).Scan(&open)
	return open, err
}

func (c *PostgresCoordinator) OpenCircuit(ctx context.Context, key string, ttl time.Duration) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO cache_circuit_breakers (key, expires_at, updated_at) VALUES ($1, now() + $2::interval, now()) ON CONFLICT (key) DO UPDATE SET expires_at=EXCLUDED.expires_at, updated_at=EXCLUDED.updated_at`, key, ttl.String())
	return err
}

func (c *PostgresCoordinator) CloseCircuit(ctx context.Context, key string) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM cache_circuit_breakers WHERE key=$1`, key)
	return err
}

func (c *PostgresCoordinator) Close() error {
	c.mu.Lock()
	leases := c.leases
	c.leases = make(map[string]*sql.Conn)
	c.mu.Unlock()
	for _, conn := range leases {
		_ = conn.Close()
	}
	return c.db.Close()
}

func newOwner() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func joinErrors(first, second error) error {
	if first != nil {
		return first
	}
	return second
}
