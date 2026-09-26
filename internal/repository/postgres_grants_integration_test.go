//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresRepositoryGrantUpsertDelete(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "integration-grants-" + uuid.NewString(), Format: FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = store.DisableHostedRepository(ctx, repo.ID) }()

	if _, err = store.UpsertRepositoryGrant(ctx, uuid.NewString(), RepositoryGrant{Principal: "user:alice", Scopes: []string{"repositories:read"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("upsert on missing repository err=%v", err)
	}

	created, err := store.UpsertRepositoryGrant(ctx, repo.ID, RepositoryGrant{Principal: "user:alice", Scopes: []string{"repositories:read"}, ResourcePrefix: "com.example"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Version != "2" || len(created.Grants) != 1 {
		t.Fatalf("created=%#v", created)
	}

	replaced, err := store.UpsertRepositoryGrant(ctx, repo.ID, RepositoryGrant{Principal: "user:alice", Scopes: []string{"repositories:read", "repositories:write"}, ResourcePrefix: "com.example"})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Version != "3" || len(replaced.Grants) != 1 || !slices.Equal(replaced.Grants[0].Scopes, []string{"repositories:read", "repositories:write"}) {
		t.Fatalf("replaced=%#v", replaced)
	}

	second, err := store.UpsertRepositoryGrant(ctx, repo.ID, RepositoryGrant{Principal: "service-account:ci", Scopes: []string{"repositories:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Grants) != 2 {
		t.Fatalf("second upsert must keep existing rows: %#v", second.Grants)
	}

	deleted, err := store.DeleteRepositoryGrant(ctx, repo.ID, "user:alice", "com.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Grants) != 1 || deleted.Grants[0].Principal != "service-account:ci" {
		t.Fatalf("deleted=%#v", deleted)
	}
	if _, err = store.DeleteRepositoryGrant(ctx, repo.ID, "user:alice", "com.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing grant err=%v", err)
	}

	loaded, err := store.GetRepositoryGrants(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != deleted.Version || len(loaded.Grants) != 1 {
		t.Fatalf("loaded=%#v", loaded)
	}
}
