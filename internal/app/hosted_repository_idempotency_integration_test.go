//go:build integration

package app

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresHostedRepositoryIdempotencySerializesFirstUse(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	type result struct {
		repo     repository.HostedRepository
		replayed bool
		err      error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			repo, replayed, err := store.CreateHostedRepositoryIdempotently(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "concurrent-native", Format: repository.FormatOCI}, "admin", "concurrent-key", "same-payload")
			results <- result{repo, replayed, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var got []result
	for result := range results {
		if result.err != nil {
			t.Fatalf("create error: %v", result.err)
		}
		got = append(got, result)
	}
	if len(got) != 2 || got[0].repo.ID != got[1].repo.ID || got[0].replayed == got[1].replayed {
		t.Fatalf("results=%#v", got)
	}
	_, _, err = store.CreateHostedRepositoryIdempotently(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "different-native", Format: repository.FormatOCI}, "admin", "concurrent-key", "different-payload")
	if err != repository.ErrIdempotencyConflict {
		t.Fatalf("different payload error=%v", err)
	}
}
