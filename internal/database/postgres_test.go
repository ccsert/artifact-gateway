package database

import (
	"testing"
	"time"
)

func TestOpenPostgresConfiguresBoundedPool(t *testing.T) {
	config := PoolConfig{MaxOpenConns: 7, MaxIdleConns: 3, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: 30 * time.Second}
	db, err := OpenPostgres("postgres://gateway:secret@localhost:5432/gateway?sslmode=disable", config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if got := db.Stats().MaxOpenConnections; got != config.MaxOpenConns {
		t.Fatalf("max open connections=%d want=%d", got, config.MaxOpenConns)
	}
}

func TestSpecializedPoolDefaultsAreBounded(t *testing.T) {
	coordinator := DefaultCoordinatorPoolConfig()
	if coordinator.MaxOpenConns != 8 || coordinator.MaxIdleConns != 2 {
		t.Fatalf("coordinator pool=%+v", coordinator)
	}
	notifications := NotificationPoolConfig()
	if notifications.MaxOpenConns != 2 || notifications.MaxIdleConns != 2 {
		t.Fatalf("notification pool=%+v", notifications)
	}
}

func TestPoolConfigRejectsUnboundedOrInconsistentLimits(t *testing.T) {
	for _, config := range []PoolConfig{
		{MaxOpenConns: 0, MaxIdleConns: 0, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute},
		{MaxOpenConns: 2, MaxIdleConns: 3, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute},
		{MaxOpenConns: 2, MaxIdleConns: 1, ConnMaxLifetime: 0, ConnMaxIdleTime: time.Minute},
	} {
		if err := config.Validate(); err == nil {
			t.Fatalf("config=%+v unexpectedly valid", config)
		}
	}
}
