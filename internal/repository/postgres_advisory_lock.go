package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"sort"
	"sync"
	"time"
)

type postgresAdvisoryLockSessionContextKey struct{}

var errPostgresAdvisoryLockSessionClosed = errors.New("postgres advisory lock session is closed")

// postgresAdvisoryLockSession lets one logical operation extend its ordered
// advisory-lock set without borrowing a second lock-pool connection. OCI carries
// the upload session into object publication; PyPI adds coordinate locks to its
// object session. Both work with a dedicated lock pool of MaxOpenConns=1.
type postgresAdvisoryLockSession struct {
	owner  *PostgresStore
	conn   *sql.Conn
	mu     sync.Mutex
	refs   int
	closed bool
}

func (s *PostgresStore) lockPostgresAdvisoryKeys(ctx context.Context, values []string) (context.Context, func(), error) {
	seen := make(map[string]struct{}, len(values))
	keys := make([]string, 0, len(values))
	for _, key := range values {
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ctx, func() {}, nil
	}

	session, _ := ctx.Value(postgresAdvisoryLockSessionContextKey{}).(*postgresAdvisoryLockSession)
	if session == nil || session.owner != s || !session.active() {
		var err error
		session, err = s.newPostgresAdvisoryLockSession(ctx)
		if err != nil {
			return ctx, nil, err
		}
		ctx = context.WithValue(ctx, postgresAdvisoryLockSessionContextKey{}, session)
	}
	release, err := session.acquire(ctx, keys)
	if errors.Is(err, errPostgresAdvisoryLockSessionClosed) {
		var openErr error
		session, openErr = s.newPostgresAdvisoryLockSession(ctx)
		if openErr != nil {
			return ctx, nil, openErr
		}
		ctx = context.WithValue(ctx, postgresAdvisoryLockSessionContextKey{}, session)
		release, err = session.acquire(ctx, keys)
	}
	if err != nil {
		session.closeIfIdle()
		return ctx, nil, err
	}
	return ctx, release, nil
}

func (s *PostgresStore) newPostgresAdvisoryLockSession(ctx context.Context) (*postgresAdvisoryLockSession, error) {
	conn, err := s.lockDB.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return &postgresAdvisoryLockSession{owner: s, conn: conn}, nil
}

func (s *postgresAdvisoryLockSession) active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed
}

func (s *postgresAdvisoryLockSession) acquire(ctx context.Context, keys []string) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errPostgresAdvisoryLockSessionClosed
	}
	acquired := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, err := s.conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, key); err != nil {
			// A cancelled/failed acquisition has an uncertain session state.
			// Abort the operation and drop the physical connection, including
			// any outer locks, instead of returning it with a possible lock.
			s.discard()
			return nil, err
		}
		acquired = append(acquired, key)
	}
	s.refs++
	var once sync.Once
	return func() {
		once.Do(func() { s.release(acquired) })
	}, nil
}

func (s *postgresAdvisoryLockSession) release(keys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for index := len(keys) - 1; index >= 0; index-- {
		if _, err := s.conn.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, keys[index]); err != nil {
			s.discard()
			return
		}
	}
	s.refs--
	if s.refs == 0 {
		s.closed = true
		_ = s.conn.Close()
	}
}

// sql.Conn.Close normally returns its session to the pool. ErrBadConn discards
// the physical PostgreSQL session, which releases all session advisory locks.
// The caller holds mu.
func (s *postgresAdvisoryLockSession) discard() {
	s.closed = true
	_ = s.conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = s.conn.Close()
}

func (s *postgresAdvisoryLockSession) closeIfIdle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.refs != 0 {
		return
	}
	s.closed = true
	_ = s.conn.Close()
}
