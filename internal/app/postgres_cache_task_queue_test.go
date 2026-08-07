package app

import (
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
)

func TestCacheTaskQueueUsesDedicatedBoundedListenerPool(t *testing.T) {
	db, err := database.OpenPostgres("postgres://gateway:password@localhost:5432/gateway", database.PoolConfig{MaxOpenConns: 3, MaxIdleConns: 1, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	queue, err := NewPostgresCacheTaskQueueWithDB(db, "postgres://gateway:password@localhost:5432/gateway")
	if err != nil {
		t.Fatal(err)
	}
	if got := queue.ListenerDatabaseStats().MaxOpenConnections; got != 2 {
		t.Fatalf("listener max open connections=%d want=2", got)
	}
	if queue.ownsDB || !queue.ownsListenerDB {
		t.Fatalf("queue ownership: database=%t listener=%t", queue.ownsDB, queue.ownsListenerDB)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if db.Stats().MaxOpenConnections != 3 {
		t.Fatal("closing task queue changed the caller-owned database pool")
	}
}

func TestCacheTaskQueueCanBorrowSharedListenerPool(t *testing.T) {
	config := database.PoolConfig{MaxOpenConns: 3, MaxIdleConns: 1, ConnMaxLifetime: time.Minute, ConnMaxIdleTime: time.Minute}
	db, err := database.OpenPostgres("postgres://gateway:password@localhost:5432/gateway", config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	listenerDB, err := database.OpenPostgres("postgres://gateway:password@localhost:5432/gateway", database.NotificationPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listenerDB.Close() }()
	queue, err := NewPostgresCacheTaskQueueWithPools(db, listenerDB)
	if err != nil {
		t.Fatal(err)
	}
	if queue.ownsDB || queue.ownsListenerDB {
		t.Fatalf("queue unexpectedly owns borrowed pools: database=%t listener=%t", queue.ownsDB, queue.ownsListenerDB)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
}
