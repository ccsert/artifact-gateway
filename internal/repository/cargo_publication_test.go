package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func cargoTestClaim(repositoryID, version, digestCharacter string) CargoIdentityClaim {
	return CargoIdentityClaim{
		RepositoryID: repositoryID, Name: "demo-crate", Version: version,
		Digest:         "sha256:" + strings.Repeat(digestCharacter, 64),
		MetadataDigest: "sha256:" + strings.Repeat("c", 64),
	}
}

func cargoTestPublication(t *testing.T, claim CargoIdentityClaim, reservation CargoIdentityReservation, size int64) CargoPublication {
	t.Helper()
	at := reservation.CreatedAt.UTC().Truncate(time.Second)
	row, err := json.Marshal(cargo.IndexEntry{
		Name: claim.Name, Version: claim.Version, Checksum: strings.TrimPrefix(claim.Digest, "sha256:"),
		Dependencies: []cargo.IndexDependency{}, Features: map[string][]string{}, PublishedAt: at.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	return CargoPublication{
		CargoIdentityClaim: claim, ObjectKey: "native/cargo/sha256/" + strings.TrimPrefix(claim.Digest, "sha256:"),
		Size: size, IndexRow: row, Publisher: "user:alice", PublishedAt: at,
	}
}

func TestMemoryCargoPublicationVisibilityRetryAndQuota(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "cargo-repo", Name: "cargo-repo", Format: FormatCargo}); err != nil {
		t.Fatal(err)
	}
	claim := cargoTestClaim("cargo-repo", "1.0.0", "a")
	reservation, _, err := store.ReserveCargoIdentity(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetCargoPublication(ctx, claim.RepositoryID, claim.Name, claim.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reservation became visible: %v", err)
	}
	publication := cargoTestPublication(t, claim, reservation, 7)
	invalid := publication
	invalid.IndexRow = []byte(`{"name":"demo-crate","vers":"1.0.0","cksum":"wrong"}`)
	if _, _, err := store.CommitCargoPublication(ctx, invalid); !errors.Is(err, ErrInvalidCargoIdentity) {
		t.Fatalf("invalid index row committed: %v", err)
	}
	if _, err := store.ReplaceRepositoryCapacityQuota(ctx, claim.RepositoryID, 5); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CommitCargoPublication(ctx, publication); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota exceeded publication committed: %v", err)
	}
	if _, err := store.GetCargoPublication(ctx, claim.RepositoryID, claim.Name, claim.Version); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed publication became visible: %v", err)
	}
	if _, err := store.ReplaceRepositoryCapacityQuota(ctx, claim.RepositoryID, 20); err != nil {
		t.Fatal(err)
	}
	first, replay, err := store.CommitCargoPublication(ctx, publication)
	if err != nil || replay {
		t.Fatalf("first=%+v replay=%t err=%v", first, replay, err)
	}
	second, replay, err := store.CommitCargoPublication(ctx, publication)
	if err != nil || !replay || !cargoPublicationMatches(first, second) {
		t.Fatalf("retry=%+v replay=%t err=%v", second, replay, err)
	}
	changed := publication
	changed.Publisher = "user:bob"
	if _, _, err := store.CommitCargoPublication(ctx, changed); !errors.Is(err, ErrCargoPublicationConflict) {
		t.Fatalf("different actor was accepted: %v", err)
	}
	listed, err := store.ListCargoPublications(ctx, claim.RepositoryID, claim.Name)
	if err != nil || len(listed) != 1 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	listed[0].IndexRow[0] = 'x'
	readback, err := store.GetCargoPublication(ctx, claim.RepositoryID, claim.Name, claim.Version)
	if err != nil || readback.IndexRow[0] == 'x' {
		t.Fatalf("stored index was mutated by caller: %v", err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, claim.RepositoryID)
	if err != nil || capacity.UsedBytes != 7 || capacity.ObjectCount != 1 {
		t.Fatalf("capacity=%+v err=%v", capacity, err)
	}
}
