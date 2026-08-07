package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
)

// PostgresCacheControlStore stores cache indexes, negative entries, and GC
// candidates in PostgreSQL while delegating immutable byte objects to S3.
// It deliberately preserves OCIObjectStore so protocol caches retain their
// tested publication ordering while their source of truth moves out of S3.
type PostgresCacheControlStore struct {
	objects OCIObjectStore
	db      *sql.DB
	ownsDB  bool
}

func NewPostgresCacheControlStore(objects OCIObjectStore, databaseURL string) (*PostgresCacheControlStore, error) {
	db, err := database.OpenPostgres(databaseURL, database.DefaultPoolConfig())
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL cache control store: %w", err)
	}
	return &PostgresCacheControlStore{objects: objects, db: db, ownsDB: true}, nil
}

func NewPostgresCacheControlStoreWithDB(objects OCIObjectStore, db *sql.DB) (*PostgresCacheControlStore, error) {
	if db == nil {
		return nil, errors.New("PostgreSQL cache control store requires a database pool")
	}
	return &PostgresCacheControlStore{objects: objects, db: db}, nil
}

func (s *PostgresCacheControlStore) Close() error {
	if !s.ownsDB {
		return nil
	}
	return s.db.Close()
}

func isCacheControlKey(key string) bool {
	for _, prefix := range []string{"oci/index/", "oci/gc/", "maven/index/", "raw/index/", "conan/index/"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func (s *PostgresCacheControlStore) Get(ctx context.Context, key string) ([]byte, error) {
	if !isCacheControlKey(key) {
		return s.objects.Get(ctx, key)
	}
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value::text FROM cache_control_entries WHERE key=$1`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errOCICacheMiss
	}
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}

func (s *PostgresCacheControlStore) Put(ctx context.Context, key string, value []byte) error {
	if !isCacheControlKey(key) {
		return s.objects.Put(ctx, key, value)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO cache_control_entries (key, value, updated_at) VALUES ($1, $2::jsonb, now()) ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=EXCLUDED.updated_at`, key, string(value))
	return err
}

func (s *PostgresCacheControlStore) Delete(ctx context.Context, key string) error {
	if !isCacheControlKey(key) {
		return s.objects.Delete(ctx, key)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM cache_control_entries WHERE key=$1`, key)
	return err
}

func (s *PostgresCacheControlStore) List(ctx context.Context, prefix string) ([]string, error) {
	if !isCacheControlKey(prefix) {
		return s.objects.List(ctx, prefix)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key FROM cache_control_entries WHERE key LIKE $1 || '%' ORDER BY key`, prefix)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *PostgresCacheControlStore) Stat(ctx context.Context, key string) (OCIObjectInfo, error) {
	return s.objects.Stat(ctx, key)
}

func (s *PostgresCacheControlStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	return s.objects.Open(ctx, key)
}

func (s *PostgresCacheControlStore) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	return s.objects.OpenRange(ctx, key, offset, length)
}

func (s *PostgresCacheControlStore) PutReader(ctx context.Context, key string, value io.Reader, size int64) error {
	return s.objects.PutReader(ctx, key, value, size)
}

func (s *PostgresCacheControlStore) PutVerifiedReader(ctx context.Context, key string, value io.Reader, size int64, digest string) error {
	return s.objects.PutVerifiedReader(ctx, key, value, size, digest)
}

func (s *PostgresCacheControlStore) SetVerifiedDigest(ctx context.Context, key, digest string) error {
	return s.objects.SetVerifiedDigest(ctx, key, digest)
}
