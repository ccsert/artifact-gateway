//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresCargoPublicationAtomicVisibilityAndQuota(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "cargo-hosted-" + uuid.NewString()[:8], Format: FormatCargo,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = store.Close()
	})
	if _, public := FormatProfileFor(FormatCargo); public {
		t.Fatal("C1 storage target was advertised before format admission")
	}
	claims := []CargoIdentityClaim{
		cargoTestClaim(repo.ID, "1.0.0", "a"),
		cargoTestClaim(repo.ID, "1.1.0", "b"),
	}
	publications := make([]CargoPublication, len(claims))
	for i, claim := range claims {
		reservation, replay, reserveErr := store.ReserveCargoIdentity(ctx, claim)
		if reserveErr != nil || replay {
			t.Fatalf("reservation %d replay=%t err=%v", i, replay, reserveErr)
		}
		publications[i] = cargoTestPublication(t, claim, reservation, 7)
		if _, readErr := store.GetCargoPublication(ctx, repo.ID, claim.Name, claim.Version); !errors.Is(readErr, ErrNotFound) {
			t.Fatalf("uncommitted version became visible: %v", readErr)
		}
	}
	if _, err := store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 10); err != nil {
		t.Fatal(err)
	}
	other, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	type result struct {
		index int
		err   error
	}
	results := make(chan result, len(publications))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index, publication := range publications {
		wait.Add(1)
		go func(index int, publication CargoPublication) {
			defer wait.Done()
			<-start
			backend := store
			if index == 1 {
				backend = other
			}
			_, _, commitErr := backend.CommitCargoPublication(ctx, publication)
			results <- result{index: index, err: commitErr}
		}(index, publication)
	}
	close(start)
	wait.Wait()
	close(results)
	winner := -1
	for outcome := range results {
		switch {
		case outcome.err == nil:
			if winner != -1 {
				t.Fatal("both versions committed past the same quota")
			}
			winner = outcome.index
		case errors.Is(outcome.err, ErrQuotaExceeded):
		default:
			t.Fatalf("unexpected commit result: %v", outcome.err)
		}
	}
	if winner < 0 {
		t.Fatal("neither version committed")
	}
	loser := 1 - winner
	if _, err := store.GetCargoPublication(ctx, repo.ID, claims[loser].Name, claims[loser].Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("quota loser became visible: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	other, err = NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := other.GetCargoPublication(ctx, repo.ID, claims[winner].Name, claims[winner].Version)
	if err != nil || !cargoPublicationMatches(stored, publications[winner]) {
		t.Fatalf("restart readback=%+v err=%v", stored, err)
	}
	_, replay, err := other.CommitCargoPublication(ctx, publications[winner])
	if err != nil || !replay {
		t.Fatalf("restart replay=%t err=%v", replay, err)
	}
	capacity, err := other.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 7 || capacity.ObjectCount != 1 {
		t.Fatalf("capacity=%+v err=%v", capacity, err)
	}
}
