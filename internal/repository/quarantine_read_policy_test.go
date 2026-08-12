package repository

import (
	"context"
	"testing"
)

func TestMemoryQuarantineReadPolicyDefaultsDisabledAndUsesOptimisticVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "raw-read-policy", Name: "raw-read-policy", Format: FormatRaw})
	if err != nil {
		t.Fatal(err)
	}

	initial, err := store.GetRepositoryQuarantineReadPolicy(ctx, repo.ID)
	if err != nil || initial.Version != "1" || initial.Enabled {
		t.Fatalf("initial=%#v err=%v", initial, err)
	}
	initial.Enabled = true
	updated, err := store.ReplaceRepositoryQuarantineReadPolicy(ctx, repo.ID, initial, "1")
	if err != nil || updated.Version != "2" || !updated.Enabled {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err = store.ReplaceRepositoryQuarantineReadPolicy(ctx, repo.ID, initial, "1"); err != ErrVersionConflict {
		t.Fatalf("stale replace err=%v", err)
	}
}
