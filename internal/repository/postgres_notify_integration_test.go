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
	locks, err := database.OpenPostgres(databaseURL, database.DefaultCoordinatorPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locks.Close() }()
	store, err := NewPostgresStoreWithPools(primary, notifications, locks)
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
	// A checked-out connection only proves the notifier's goroutine has started:
	// it issues LISTEN after acquiring the connection, so a notification sent in
	// between precedes the subscription and PostgreSQL does not replay it for a
	// listener that had not subscribed yet. Callers tolerate that - every one of
	// them polls on a ticker as well - so keep notifying until the channel
	// answers rather than assuming the first one arrives.
	delivered := func(channel string, wake <-chan struct{}) bool {
		deliveryDeadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deliveryDeadline) {
			if _, err := primary.Exec(`SELECT pg_notify($1, '')`, channel); err != nil {
				t.Fatal(err)
			}
			select {
			case <-wake:
				return true
			case <-time.After(250 * time.Millisecond):
			}
		}
		return false
	}
	for channel, wake := range map[string]<-chan struct{}{
		"artifact_gateway_lifecycle_jobs":    lifecycle,
		"artifact_gateway_replication_plans": replication,
	} {
		if !delivered(channel, wake) {
			t.Fatalf("notification %q was not delivered", channel)
		}
	}
}
