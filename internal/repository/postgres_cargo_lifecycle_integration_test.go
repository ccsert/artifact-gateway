//go:build integration

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresCargoTombstoneLifecycle(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	repositoryID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repositoryID)
		_ = store.Close()
	})
	checkCargoTombstoneLifecycle(t, store, repositoryID, "cargo-life-"+uuid.NewString()[:8])
}
