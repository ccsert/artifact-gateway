// Package database owns PostgreSQL connection-pool construction shared by the
// runtime adapters.
package database

import (
	"database/sql"
	"errors"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    32,
		MaxIdleConns:    8,
		ConnMaxLifetime: 30 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
	}
}

func DefaultCoordinatorPoolConfig() PoolConfig {
	config := DefaultPoolConfig()
	config.MaxOpenConns = 8
	config.MaxIdleConns = 2
	return config
}

func NotificationPoolConfig() PoolConfig {
	config := DefaultPoolConfig()
	config.MaxOpenConns = 2
	config.MaxIdleConns = 2
	return config
}

func (c PoolConfig) Validate() error {
	if c.MaxOpenConns <= 0 {
		return errors.New("database maximum open connections must be positive")
	}
	if c.MaxIdleConns < 0 || c.MaxIdleConns > c.MaxOpenConns {
		return errors.New("database maximum idle connections must be between zero and maximum open connections")
	}
	if c.ConnMaxLifetime <= 0 || c.ConnMaxIdleTime <= 0 {
		return errors.New("database connection lifetime and idle time must be positive")
	}
	return nil
}

func OpenPostgres(databaseURL string, config PoolConfig) (*sql.DB, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	ConfigurePool(db, config)
	return db, nil
}

func ConfigurePool(db *sql.DB, config PoolConfig) {
	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime)
}
