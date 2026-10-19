//go:build integration

package repository_test

import (
	"context"
	"os"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestRawPostgresStoreContract(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL contract tests")
	}
	runRawStoreContract(t, func(t *testing.T) (repository.NativeRawStore, string) {
		t.Helper()
		store, err := repository.NewPostgresStore(databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		})
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-contract-" + uuid.NewString()[:8], Format: repository.FormatRaw})
		if err != nil {
			t.Fatal(err)
		}
		return store, repo.ID
	})
}
