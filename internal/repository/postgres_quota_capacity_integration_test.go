//go:build integration

package repository

import (
	"context"
	"github.com/google/uuid"
	"os"
	"testing"
)

func TestPostgresCapacityListExcludesCollectedCargoBytes(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	s, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()
	repo, err := s.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "cargo-capacity-" + uuid.NewString(), Format: FormatCargo})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = s.db.Exec(`DELETE FROM hosted_repositories WHERE id=$1`, repo.ID) }()
	claim := cargoTestClaim(repo.ID, "1.0.0", "a")
	reservation, _, err := s.ReserveCargoIdentity(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	p := cargoTestPublication(t, claim, reservation, 7)
	if _, _, err = s.CommitCargoPublication(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TombstoneCargoPublication(ctx, repo.ID, p.Name, p.Version); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkCargoObjectCollecting(ctx, p.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkCargoObjectCollected(ctx, p.ObjectKey); err != nil {
		t.Fatal(err)
	}
	capacity, err := s.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 0 {
		t.Fatalf("collected individual capacity: %#v %v", capacity, err)
	}
	records, err := s.ListRepositoryCapacityRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Repository.ID == repo.ID {
			if r.Capacity.UsedBytes != 0 || r.Capacity.ObjectCount != 0 {
				t.Fatalf("collected Cargo inflates quota evidence: %#v", r.Capacity)
			}
			return
		}
	}
	t.Fatal("capacity row missing")
}
