package repository

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestMemoryCargoIdentityReservationConflictsAndRetries(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, repositoryID := range []string{"first", "second"} {
		if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: repositoryID, Name: repositoryID, Format: FormatRaw}); err != nil {
			t.Fatal(err)
		}
	}
	claim := CargoIdentityClaim{RepositoryID: "first", Name: "demo-crate", Version: "1.2.3+build.1", Digest: "sha256:" + strings.Repeat("a", 64), MetadataDigest: "sha256:" + strings.Repeat("c", 64)}
	first, replay, err := store.ReserveCargoIdentity(ctx, claim)
	if err != nil || replay || first.CollisionKey != "demo-crate" || first.VersionKey != "1.2.3" {
		t.Fatalf("first reservation=%+v replay=%t err=%v", first, replay, err)
	}
	second, replay, err := store.ReserveCargoIdentity(ctx, claim)
	if err != nil || !replay || second != first {
		t.Fatalf("exact retry=%+v replay=%t err=%v", second, replay, err)
	}
	stored, err := store.GetCargoIdentityReservation(ctx, "first", "demo-crate", "1.2.3+build.1")
	if err != nil || stored != first {
		t.Fatalf("readback=%+v err=%v", stored, err)
	}
	for _, conflicting := range []CargoIdentityClaim{
		{RepositoryID: "first", Name: "Demo_Crate", Version: claim.Version, Digest: claim.Digest, MetadataDigest: claim.MetadataDigest},
		{RepositoryID: "first", Name: claim.Name, Version: "1.2.3+build.2", Digest: claim.Digest, MetadataDigest: claim.MetadataDigest},
		{RepositoryID: "first", Name: claim.Name, Version: claim.Version, Digest: "sha256:" + strings.Repeat("b", 64), MetadataDigest: claim.MetadataDigest},
		{RepositoryID: "first", Name: claim.Name, Version: claim.Version, Digest: claim.Digest, MetadataDigest: "sha256:" + strings.Repeat("d", 64)},
	} {
		if _, _, err := store.ReserveCargoIdentity(ctx, conflicting); !errors.Is(err, ErrCargoIdentityConflict) {
			t.Fatalf("conflicting claim=%+v err=%v", conflicting, err)
		}
	}
	claim.Version = "1.2.4"
	if _, replay, err := store.ReserveCargoIdentity(ctx, claim); err != nil || replay {
		t.Fatalf("new version replay=%t err=%v", replay, err)
	}
	claim.RepositoryID = "second"
	claim.Name = "Demo_Crate"
	if _, replay, err := store.ReserveCargoIdentity(ctx, claim); err != nil || replay {
		t.Fatalf("different repository replay=%t err=%v", replay, err)
	}
}

func TestMemoryCargoIdentityReservationRejectsInvalidInput(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "repo", Name: "repo", Format: FormatRaw}); err != nil {
		t.Fatal(err)
	}
	valid := CargoIdentityClaim{RepositoryID: "repo", Name: "demo", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64), MetadataDigest: "sha256:" + strings.Repeat("c", 64)}
	for _, invalid := range []CargoIdentityClaim{
		{RepositoryID: "missing", Name: valid.Name, Version: valid.Version, Digest: valid.Digest, MetadataDigest: valid.MetadataDigest},
		{RepositoryID: valid.RepositoryID, Name: "bad/name", Version: valid.Version, Digest: valid.Digest, MetadataDigest: valid.MetadataDigest},
		{RepositoryID: valid.RepositoryID, Name: valid.Name, Version: "not-a-version", Digest: valid.Digest, MetadataDigest: valid.MetadataDigest},
		{RepositoryID: valid.RepositoryID, Name: valid.Name, Version: valid.Version, Digest: "sha256:broken", MetadataDigest: valid.MetadataDigest},
		{RepositoryID: valid.RepositoryID, Name: valid.Name, Version: valid.Version, Digest: valid.Digest, MetadataDigest: "sha256:broken"},
	} {
		if _, _, err := store.ReserveCargoIdentity(ctx, invalid); err == nil {
			t.Fatalf("invalid claim accepted: %+v", invalid)
		}
	}
	if _, err := store.GetCargoIdentityReservation(ctx, "repo", valid.Name, valid.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid claims left a reservation: %v", err)
	}
}

func TestMemoryCargoIdentityReservationConcurrentRetry(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "repo", Name: "repo", Format: FormatRaw}); err != nil {
		t.Fatal(err)
	}
	claim := CargoIdentityClaim{RepositoryID: "repo", Name: "demo", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64), MetadataDigest: "sha256:" + strings.Repeat("c", 64)}
	const workers = 24
	var wait sync.WaitGroup
	results := make(chan bool, workers)
	errorsCh := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, replay, err := store.ReserveCargoIdentity(ctx, claim)
			results <- replay
			errorsCh <- err
		}()
	}
	wait.Wait()
	close(results)
	close(errorsCh)
	created := 0
	for replay := range results {
		if !replay {
			created++
		}
	}
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if created != 1 {
		t.Fatalf("new reservations=%d, want 1", created)
	}
}
