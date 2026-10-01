//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresCargoIdentityReservationAcrossConnections(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	firstStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		_ = firstStore.Close()
		t.Fatal(err)
	}
	ctx := context.Background()
	// C0 has no public Cargo format. An existing repository is only a private
	// namespace fixture for exercising the reservation table's FK and locks.
	repo, err := firstStore.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "cargo-c0-" + uuid.NewString()[:8], Format: FormatRaw,
	})
	if err != nil {
		_ = firstStore.Close()
		_ = secondStore.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = firstStore.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = firstStore.Close()
		_ = secondStore.Close()
	})
	claim := CargoIdentityClaim{RepositoryID: repo.ID, Name: "demo-crate", Version: "1.2.3+build.1", Digest: "sha256:" + strings.Repeat("a", 64), MetadataDigest: "sha256:" + strings.Repeat("c", 64)}
	first, replay, err := firstStore.ReserveCargoIdentity(ctx, claim)
	if err != nil || replay {
		t.Fatalf("first=%+v replay=%t err=%v", first, replay, err)
	}
	if err := secondStore.Close(); err != nil {
		t.Fatal(err)
	}
	secondStore, err = NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := secondStore.GetCargoIdentityReservation(ctx, repo.ID, claim.Name, claim.Version)
	if err != nil || stored != first {
		t.Fatalf("cross-connection read=%+v err=%v", stored, err)
	}
	second, replay, err := secondStore.ReserveCargoIdentity(ctx, claim)
	if err != nil || !replay || second != first {
		t.Fatalf("exact retry=%+v replay=%t err=%v", second, replay, err)
	}
	for _, conflicting := range []CargoIdentityClaim{
		{RepositoryID: repo.ID, Name: "Demo_Crate", Version: claim.Version, Digest: claim.Digest, MetadataDigest: claim.MetadataDigest},
		{RepositoryID: repo.ID, Name: claim.Name, Version: "1.2.3+build.2", Digest: claim.Digest, MetadataDigest: claim.MetadataDigest},
		{RepositoryID: repo.ID, Name: claim.Name, Version: claim.Version, Digest: "sha256:" + strings.Repeat("b", 64), MetadataDigest: claim.MetadataDigest},
		{RepositoryID: repo.ID, Name: claim.Name, Version: claim.Version, Digest: claim.Digest, MetadataDigest: "sha256:" + strings.Repeat("d", 64)},
	} {
		if _, _, err := secondStore.ReserveCargoIdentity(ctx, conflicting); !errors.Is(err, ErrCargoIdentityConflict) {
			t.Fatalf("claim=%+v err=%v", conflicting, err)
		}
	}
	var nameCount, versionCount int
	if err := firstStore.db.QueryRowContext(ctx, `SELECT count(*) FROM native_cargo_names WHERE repository_id=$1`, repo.ID).Scan(&nameCount); err != nil {
		t.Fatal(err)
	}
	if err := firstStore.db.QueryRowContext(ctx, `SELECT count(*) FROM native_cargo_identity_reservations WHERE repository_id=$1`, repo.ID).Scan(&versionCount); err != nil {
		t.Fatal(err)
	}
	if nameCount != 1 || versionCount != 1 {
		t.Fatalf("conflicts left partial rows: names=%d versions=%d", nameCount, versionCount)
	}
}

func TestPostgresCargoIdentityReservationConcurrentCollision(t *testing.T) {
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
		ID: uuid.NewString(), Name: "cargo-race-" + uuid.NewString()[:8], Format: FormatRaw,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = store.Close()
	})
	claims := []CargoIdentityClaim{
		{RepositoryID: repo.ID, Name: "demo-crate", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64), MetadataDigest: "sha256:" + strings.Repeat("c", 64)},
		{RepositoryID: repo.ID, Name: "Demo_Crate", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("b", 64), MetadataDigest: "sha256:" + strings.Repeat("d", 64)},
	}
	const workers = 12
	start := make(chan struct{})
	results := make(chan error, workers)
	var wait sync.WaitGroup
	for index := range workers {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, _, reserveErr := store.ReserveCargoIdentity(ctx, claims[index%len(claims)])
			results <- reserveErr
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrCargoIdentityConflict):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent error: %v", err)
		}
	}
	if succeeded != workers/2 || conflicted != workers/2 {
		t.Fatalf("success=%d conflicts=%d", succeeded, conflicted)
	}
	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM native_cargo_identity_reservations WHERE repository_id=$1`, repo.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("winner count=%d err=%v", count, err)
	}
}
