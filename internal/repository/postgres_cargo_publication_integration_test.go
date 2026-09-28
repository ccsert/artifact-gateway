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
	changed, didChange, err := other.SetCargoYanked(ctx, repo.ID, claims[winner].Name, claims[winner].Version, true)
	if err != nil || !didChange || !changed.Yanked || changed.Digest != publications[winner].Digest {
		t.Fatalf("yank=%+v changed=%t err=%v", changed, didChange, err)
	}
	searchResults, total, err := store.SearchCargoCrates(ctx, repo.ID, "demo", 10, "", false)
	if err != nil || total != 0 || len(searchResults) != 0 {
		t.Fatalf("yanked crate appeared in search: %+v total=%d err=%v", searchResults, total, err)
	}
	managed, total, err := store.SearchCargoCrates(ctx, repo.ID, "demo", 10, "", true)
	if err != nil || total != 1 || len(managed) != 1 || managed[0].Name != "demo-crate" {
		t.Fatalf("management search lost yanked crate: %+v total=%d err=%v", managed, total, err)
	}
	if _, _, err := store.SetCargoYanked(ctx, repo.ID, claims[winner].Name, claims[winner].Version, false); err != nil {
		t.Fatal(err)
	}
	searchResults, total, err = other.SearchCargoCrates(ctx, repo.ID, "demo", 10, "", false)
	if err != nil || total != 1 || len(searchResults) != 1 || searchResults[0].MaxVersion != claims[winner].Version {
		t.Fatalf("unyanked search: %+v total=%d err=%v", searchResults, total, err)
	}
	root, err := other.ListArtifactBrowseNodes(ctx, repo.ID, FormatCargo, ArtifactBrowseParent{}, 10, "")
	if err != nil || len(root) != 1 || root[0].Kind != BrowseNodeComponent || root[0].Name != "demo-crate" {
		t.Fatalf("Cargo browse root=%+v err=%v", root, err)
	}
	versions, err := other.ListArtifactBrowseNodes(ctx, repo.ID, FormatCargo, ArtifactBrowseParent{
		Kind: BrowseNodeComponent, Component: root[0].Name,
	}, 10, "")
	if err != nil || len(versions) != 1 || versions[0].Coordinate != "demo-crate@"+claims[winner].Version {
		t.Fatalf("Cargo browse versions=%+v err=%v", versions, err)
	}
	assets, err := other.ListArtifactBrowseNodes(ctx, repo.ID, FormatCargo, ArtifactBrowseParent{
		Kind: BrowseNodeVersion, Component: root[0].Name, Version: versions[0].Coordinate,
	}, 10, "")
	if err != nil || len(assets) != 1 || assets[0].Digest != claims[winner].Digest {
		t.Fatalf("Cargo browse assets=%+v err=%v", assets, err)
	}
	if _, err := store.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 30); err != nil {
		t.Fatal(err)
	}
	helper := cargoTestClaim(repo.ID, "0.1.0", "d")
	helper.Name = "demo-helper"
	helperReservation, _, err := store.ReserveCargoIdentity(ctx, helper)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CommitCargoPublication(ctx, cargoTestPublication(t, helper, helperReservation, 7)); err != nil {
		t.Fatal(err)
	}
	firstPage, total, err := other.SearchCargoCrates(ctx, repo.ID, "demo", 1, "", false)
	if err != nil || total != 2 || len(firstPage) != 1 || firstPage[0].Name != "demo-crate" {
		t.Fatalf("first PostgreSQL Cargo page=%+v total=%d err=%v", firstPage, total, err)
	}
	secondPage, total, err := other.SearchCargoCrates(ctx, repo.ID, "demo", 1, firstPage[0].Name, false)
	if err != nil || total != 2 || len(secondPage) != 1 || secondPage[0].Name != "demo-helper" {
		t.Fatalf("second PostgreSQL Cargo page=%+v total=%d err=%v", secondPage, total, err)
	}
}
