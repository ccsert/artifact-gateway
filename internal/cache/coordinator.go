// Package cache owns cross-instance cache coordination.
package cache

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
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
	db          *sql.DB
	stateDB     *sql.DB
	ownsDB      bool
	ownsStateDB bool
	mu          sync.Mutex
	leases      map[string]postgresLease
}

type postgresLease struct {
	key  string
	conn *sql.Conn
}

func NewPostgresCoordinator(databaseURL string) (*PostgresCoordinator, error) {
	db, err := database.OpenPostgres(databaseURL, database.DefaultCoordinatorPoolConfig())
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL cache coordinator: %w", err)
	}
	stateDB, err := database.OpenPostgres(databaseURL, database.DefaultPoolConfig())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open PostgreSQL cache coordinator state: %w", err)
	}
	return &PostgresCoordinator{db: db, stateDB: stateDB, ownsDB: true, ownsStateDB: true, leases: make(map[string]postgresLease)}, nil
}

func NewPostgresCoordinatorWithPools(lockDB, stateDB *sql.DB) (*PostgresCoordinator, error) {
	if lockDB == nil || stateDB == nil {
		return nil, errors.New("PostgreSQL cache coordinator requires lock and state database pools")
	}
	return &PostgresCoordinator{db: lockDB, stateDB: stateDB, leases: make(map[string]postgresLease)}, nil
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
	lockKey := "artifact-gateway:cache:" + key
	err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, lockKey).Scan(&locked)
	if err != nil || !locked {
		_ = conn.Close()
		return owner, false, err
	}
	c.mu.Lock()
	c.leases[owner] = postgresLease{key: lockKey, conn: conn}
	c.mu.Unlock()
	return owner, true, nil
}

func (c *PostgresCoordinator) Renew(ctx context.Context, _ string, owner string, _ time.Duration) (bool, error) {
	c.mu.Lock()
	lease, exists := c.leases[owner]
	c.mu.Unlock()
	if !exists {
		return false, nil
	}
	if err := lease.conn.PingContext(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (c *PostgresCoordinator) Release(ctx context.Context, _ string, owner string) error {
	c.mu.Lock()
	lease, exists := c.leases[owner]
	delete(c.leases, owner)
	c.mu.Unlock()
	if !exists {
		return nil
	}
	return releasePostgresLease(ctx, lease)
}

func (c *PostgresCoordinator) CircuitOpen(ctx context.Context, key string) (bool, error) {
	var open bool
	err := c.stateDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM cache_circuit_breakers WHERE key=$1 AND expires_at > now())`, key).Scan(&open)
	return open, err
}

func (c *PostgresCoordinator) OpenCircuit(ctx context.Context, key string, ttl time.Duration) error {
	_, err := c.stateDB.ExecContext(ctx, `INSERT INTO cache_circuit_breakers (key, expires_at, updated_at) VALUES ($1, now() + $2::interval, now()) ON CONFLICT (key) DO UPDATE SET expires_at=EXCLUDED.expires_at, updated_at=EXCLUDED.updated_at`, key, ttl.String())
	return err
}

func (c *PostgresCoordinator) CloseCircuit(ctx context.Context, key string) error {
	_, err := c.stateDB.ExecContext(ctx, `DELETE FROM cache_circuit_breakers WHERE key=$1`, key)
	return err
}

func (c *PostgresCoordinator) Close() error {
	c.mu.Lock()
	leases := c.leases
	c.leases = make(map[string]postgresLease)
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var closeErr error
	for _, lease := range leases {
		closeErr = joinErrors(closeErr, releasePostgresLease(ctx, lease))
	}
	if c.ownsDB {
		closeErr = joinErrors(closeErr, c.db.Close())
	}
	if c.ownsStateDB {
		closeErr = joinErrors(closeErr, c.stateDB.Close())
	}
	return closeErr
}

func releasePostgresLease(ctx context.Context, lease postgresLease) error {
	var unlocked bool
	err := lease.conn.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lease.key).Scan(&unlocked)
	if err == nil && !unlocked {
		err = errors.New("PostgreSQL cache advisory lock was not held")
	}
	if err != nil {
		_ = lease.conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return joinErrors(err, lease.conn.Close())
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
