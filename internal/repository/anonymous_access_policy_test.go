package repository

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryAnonymousAccessPolicyDefaultsDisabledAndUsesOptimisticVersioning(t *testing.T) {
	store := NewMemoryStore()
	policy, err := store.GetAnonymousAccessPolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if policy.Enabled || policy.Version != "1" {
		t.Fatalf("default policy = %#v, want disabled version 1", policy)
	}
	updated, err := store.ReplaceAnonymousAccessPolicy(context.Background(), AnonymousAccessPolicy{Enabled: true}, policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Enabled || updated.Version != "2" {
		t.Fatalf("updated policy = %#v, want enabled version 2", updated)
	}
	if _, err := store.ReplaceAnonymousAccessPolicy(context.Background(), AnonymousAccessPolicy{}, policy.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale replace error = %v, want ErrVersionConflict", err)
	}
}
