package repository

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct {
	db       *sql.DB
	ownsDB   bool
	notifier *postgresNotifier
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
	return &PostgresStore{db: db, ownsDB: true, notifier: newPostgresNotifier(listenerDB, true)}, nil
}

func NewPostgresStoreWithPools(db, listenerDB *sql.DB) (*PostgresStore, error) {
	if db == nil || listenerDB == nil {
		return nil, errors.New("postgres store requires database and notification pools")
	}
	return &PostgresStore{db: db, notifier: newPostgresNotifier(listenerDB, false)}, nil
}

func (s *PostgresStore) Close() error {
	var notifyErr error
	if s.notifier != nil {
		notifyErr = s.notifier.Close()
	}
	if s.ownsDB {
		return errors.Join(notifyErr, s.db.Close())
	}
	return notifyErr
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
