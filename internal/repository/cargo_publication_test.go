package repository

import (
	"bytes"
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

func TestMemoryCargoSearchAndYankPreservePublishedBytes(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "cargo-search", Name: "cargo-search", Format: FormatCargo}); err != nil {
		t.Fatal(err)
	}
	versions := []string{"1.2.0", "1.10.0"}
	publications := make([]CargoPublication, 0, len(versions))
	for index, version := range versions {
		claim := cargoTestClaim("cargo-search", version, []string{"a", "b"}[index])
		reservation, _, err := store.ReserveCargoIdentity(ctx, claim)
		if err != nil {
			t.Fatal(err)
		}
		publication := cargoTestPublication(t, claim, reservation, 7)
		publication.Description = "description for " + version
		if _, _, err := store.CommitCargoPublication(ctx, publication); err != nil {
			t.Fatal(err)
		}
		publications = append(publications, publication)
	}
	checkSearch := func(expected string, total int) {
		t.Helper()
		items, count, err := store.SearchCargoCrates(ctx, "cargo-search", "demo", 10, "", false)
		if err != nil || count != total || len(items) != total {
			t.Fatalf("search items=%+v total=%d err=%v", items, count, err)
		}
		if total > 0 && (items[0].MaxVersion != expected || items[0].Description != "description for "+expected) {
			t.Fatalf("search chose %+v, want %s", items[0], expected)
		}
	}
	checkSearch("1.10.0", 1)
	before, err := store.GetCargoPublication(ctx, "cargo-search", "demo-crate", "1.10.0")
	if err != nil {
		t.Fatal(err)
	}
	changed, didChange, err := store.SetCargoYanked(ctx, "cargo-search", "demo-crate", "1.10.0", true)
	if err != nil || !didChange || !changed.Yanked || !bytes.Equal(changed.IndexRow, before.IndexRow) || changed.Digest != before.Digest {
		t.Fatalf("yank changed immutable publication: %+v changed=%t err=%v", changed, didChange, err)
	}
	checkSearch("1.2.0", 1)
	if _, didChange, err := store.SetCargoYanked(ctx, "cargo-search", "demo-crate", "1.10.0", true); err != nil || didChange {
		t.Fatalf("repeated yank changed=%t err=%v", didChange, err)
	}
	if _, didChange, err := store.CommitCargoPublication(ctx, publications[1]); err != nil || !didChange {
		t.Fatalf("exact publish replay after yank=%t err=%v", didChange, err)
	}
	if _, _, err := store.SetCargoYanked(ctx, "cargo-search", "demo-crate", "1.2.0", true); err != nil {
		t.Fatal(err)
	}
	checkSearch("", 0)
	managed, count, err := store.SearchCargoCrates(ctx, "cargo-search", "demo", 10, "", true)
	if err != nil || count != 1 || len(managed) != 1 || managed[0].MaxVersion != "1.10.0" {
		t.Fatalf("management search must retain fully yanked crate: %+v total=%d err=%v", managed, count, err)
	}
	if _, _, err := store.SetCargoYanked(ctx, "cargo-search", "demo-crate", "1.10.0", false); err != nil {
		t.Fatal(err)
	}
	checkSearch("1.10.0", 1)
	helper := cargoTestClaim("cargo-search", "0.1.0", "d")
	helper.Name = "demo-helper"
	reservation, _, err := store.ReserveCargoIdentity(ctx, helper)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CommitCargoPublication(ctx, cargoTestPublication(t, helper, reservation, 7)); err != nil {
		t.Fatal(err)
	}
	first, total, err := store.SearchCargoCrates(ctx, "cargo-search", "demo", 1, "", false)
	if err != nil || total != 2 || len(first) != 1 || first[0].Name != "demo-crate" {
		t.Fatalf("first Cargo search page=%+v total=%d err=%v", first, total, err)
	}
	second, total, err := store.SearchCargoCrates(ctx, "cargo-search", "demo", 1, first[0].Name, false)
	if err != nil || total != 2 || len(second) != 1 || second[0].Name != "demo-helper" {
		t.Fatalf("second Cargo search page=%+v total=%d err=%v", second, total, err)
	}
}
