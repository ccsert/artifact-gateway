//go:build integration

package app

import (
	"os"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestPostgresMigrationCapacityPreflightConsumesGatewayReadOnlySnapshots(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	testMigrationCapacityPreflight(t, store)
}
