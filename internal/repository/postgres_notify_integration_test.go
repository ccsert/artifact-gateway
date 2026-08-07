//go:build integration

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/database"
)

func TestPostgresNotifierMultiplexesChannelsOnOneConnection(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PostgreSQL integration environment is required")
	}
	primary, err := database.OpenPostgres(databaseURL, database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = primary.Close() }()
	notifications, err := database.OpenPostgres(databaseURL, database.NotificationPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = notifications.Close() }()
	store, err := NewPostgresStoreWithPools(primary, notifications)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lifecycle := store.Listen(ctx, "artifact_gateway_lifecycle_jobs")
	replication := store.Listen(ctx, "artifact_gateway_replication_plans")
	deadline := time.Now().Add(5 * time.Second)
	for notifications.Stats().InUse != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := notifications.Stats().InUse; got != 1 {
		t.Fatalf("notification connections in use=%d want=1", got)
	}
	for channel, wake := range map[string]<-chan struct{}{
		"artifact_gateway_lifecycle_jobs":    lifecycle,
		"artifact_gateway_replication_plans": replication,
	} {
		if _, err := primary.Exec(`SELECT pg_notify($1, '')`, channel); err != nil {
			t.Fatal(err)
		}
		select {
		case <-wake:
		case <-time.After(5 * time.Second):
			t.Fatalf("notification %q was not delivered", channel)
		}
	}
}
