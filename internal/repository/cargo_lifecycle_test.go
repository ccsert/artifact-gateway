package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

type cargoLifecycleTestStore interface {
	NativeCargoStore
	HostedRepositoryStore
	RepositoryCapacityStore
	ArtifactTombstoneStore
}

func checkCargoTombstoneLifecycle(t *testing.T, store cargoLifecycleTestStore, repositoryID, repositoryName string) {
	t.Helper()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: repositoryID, Name: repositoryName, Format: FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	claim := cargoTestClaim(repo.ID, "1.0.0", "a")
	reservation, _, err := store.ReserveCargoIdentity(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	publication := cargoTestPublication(t, claim, reservation, 7)
	if _, _, err := store.CommitCargoPublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.SetCargoYanked(ctx, repo.ID, claim.Name, claim.Version, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCargoPublication(ctx, repo.ID, claim.Name, claim.Version); err != nil {
		t.Fatalf("yank hid the crate archive: %v", err)
	}
	if _, err := store.TombstoneCargoPublication(ctx, repo.ID, claim.Name, claim.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCargoPublication(ctx, repo.ID, claim.Name, claim.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tombstoned crate visible: %v", err)
	}
	if versions, err := store.ListCargoPublications(ctx, repo.ID, claim.Name); err != nil && !errors.Is(err, ErrNotFound) || len(versions) != 0 {
		t.Fatalf("tombstoned index versions=%+v err=%v", versions, err)
	}
	if _, _, err := store.SetCargoYanked(ctx, repo.ID, claim.Name, claim.Version, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("yank changed tombstone: %v", err)
	}
	if _, _, err := store.CommitCargoPublication(ctx, publication); !errors.Is(err, ErrArtifactTombstoned) {
		t.Fatalf("republished tombstone: %v", err)
	}
	if used, err := store.GetRepositoryCapacity(ctx, repo.ID); err != nil || used.UsedBytes != 7 {
		t.Fatalf("recoverable bytes lost from capacity: %+v err=%v", used, err)
	}
	if _, err := store.RestoreCargoPublication(ctx, repo.ID, claim.Name, claim.Version); err != nil {
		t.Fatal(err)
	}
	restored, err := store.GetCargoPublication(ctx, repo.ID, claim.Name, claim.Version)
	if err != nil || !restored.Yanked || restored.Digest != claim.Digest {
		t.Fatalf("restored publication=%+v err=%v", restored, err)
	}
	if _, err := store.TombstoneCargoPublication(ctx, repo.ID, claim.Name, claim.Version); err != nil {
		t.Fatal(err)
	}
	objects, err := store.ListReclaimableCargoObjects(ctx, time.Now().UTC().Add(time.Hour), 10, "")
	if err != nil || len(objects) != 1 || objects[0].ObjectKey != publication.ObjectKey {
		t.Fatalf("reclaim candidates=%+v err=%v", objects, err)
	}
	if _, err := store.RestoreCargoPublication(ctx, repo.ID, claim.Name, claim.Version); err != nil {
		t.Fatal(err)
	}
	if matches, err := store.CargoObjectMatchesTombstone(ctx, publication.ObjectKey, objects[0].TombstonedAt); err != nil || matches {
		t.Fatalf("stale reclaim generation matched=%t err=%v", matches, err)
	}
	if _, err := store.TombstoneCargoPublication(ctx, repo.ID, claim.Name, claim.Version); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCargoObjectCollecting(ctx, publication.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreCargoPublication(ctx, repo.ID, claim.Name, claim.Version); !errors.Is(err, ErrDisabled) {
		t.Fatalf("restored while collecting: %v", err)
	}
	if err := store.MarkCargoObjectCollected(ctx, publication.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreCargoPublication(ctx, repo.ID, claim.Name, claim.Version); !errors.Is(err, ErrDisabled) {
		t.Fatalf("restored after collection: %v", err)
	}
	if used, err := store.GetRepositoryCapacity(ctx, repo.ID); err != nil || used.UsedBytes != 0 || used.ObjectCount != 0 {
		t.Fatalf("collected bytes still count toward quota: %+v err=%v", used, err)
	}
}

func TestMemoryCargoTombstoneLifecycle(t *testing.T) {
	checkCargoTombstoneLifecycle(t, NewMemoryStore(), "cargo-lifecycle", "cargo-lifecycle")
}
