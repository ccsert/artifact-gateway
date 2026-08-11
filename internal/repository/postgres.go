package repository

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct {
	db         *sql.DB
	lockDB     *sql.DB
	ownsDB     bool
	ownsLockDB bool
	notifier   *postgresNotifier
}

func NewPostgresStore(databaseURL string) (*PostgresStore, error) {
	db, err := database.OpenPostgres(databaseURL, database.DefaultPoolConfig())
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	listenerDB, err := database.OpenPostgres(databaseURL, database.NotificationPoolConfig())
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open postgres notification pool: %w", err)
	}
	lockDB, err := database.OpenPostgres(databaseURL, database.DefaultArtifactLockPoolConfig())
	if err != nil {
		_ = listenerDB.Close()
		_ = db.Close()
		return nil, fmt.Errorf("open postgres artifact lock pool: %w", err)
	}
	return &PostgresStore{db: db, lockDB: lockDB, ownsDB: true, ownsLockDB: true, notifier: newPostgresNotifier(listenerDB, true)}, nil
}

func NewPostgresStoreWithPools(db, listenerDB, lockDB *sql.DB) (*PostgresStore, error) {
	if db == nil || listenerDB == nil || lockDB == nil {
		return nil, errors.New("postgres store requires database, notification, and artifact lock pools")
	}
	return &PostgresStore{db: db, lockDB: lockDB, notifier: newPostgresNotifier(listenerDB, false)}, nil
}

func (s *PostgresStore) Close() error {
	var notifyErr error
	if s.notifier != nil {
		notifyErr = s.notifier.Close()
	}
	var lockErr error
	if s.ownsLockDB && s.lockDB != nil {
		lockErr = s.lockDB.Close()
	}
	if s.ownsDB {
		return errors.Join(notifyErr, lockErr, s.db.Close())
	}
	return errors.Join(notifyErr, lockErr)
}

func isUnique(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}

func IsQuotaExceeded(err error) bool {
	if errors.Is(err, ErrQuotaExceeded) {
		return true
	}
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "P0001" && postgresError.Message == ErrQuotaExceeded.Error()
}
