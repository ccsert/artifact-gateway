//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresAuthorizationRoleLifecycle(t *testing.T) {
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
	name := "integration-role-" + strings.ToLower(uuid.NewString())
	created, err := store.CreateAuthorizationRole(ctx, AuthorizationRole{
		Name:        "  " + name + "  ",
		Description: "PostgreSQL authorization role integration test",
		Scopes:      []string{"repositories:read", "repositories:intelligence"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteAuthorizationRole(ctx, created.ID) }()
	if created.Name != name || created.Version != "1" || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created=%#v", created)
	}
	if !slices.Equal(created.Scopes, []string{"repositories:read", "repositories:intelligence"}) {
		t.Fatalf("created scopes=%#v", created.Scopes)
	}

	loaded, err := store.GetAuthorizationRole(ctx, created.ID)
	if err != nil || !slices.Equal(loaded.Scopes, created.Scopes) {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	listed, err := store.ListAuthorizationRoles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(listed, func(role AuthorizationRole) bool { return role.ID == created.ID }) {
		t.Fatalf("created role missing from list: %#v", listed)
	}

	_, err = store.CreateAuthorizationRole(ctx, AuthorizationRole{
		Name:   strings.ToUpper(name),
		Scopes: []string{"repositories:read"},
	})
	if !errors.Is(err, ErrAuthorizationRoleNameExists) {
		t.Fatalf("case-insensitive duplicate err=%v", err)
	}

	updated, err := store.UpdateAuthorizationRole(ctx, AuthorizationRole{
		ID:          created.ID,
		Name:        name + "-updated",
		Description: "updated role",
		Scopes:      []string{"repositories:write", "repositories:intelligence"},
	}, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != "2" || !slices.Equal(updated.Scopes, []string{"repositories:write", "repositories:intelligence"}) {
		t.Fatalf("updated=%#v", updated)
	}
	if _, err = store.UpdateAuthorizationRole(ctx, updated, created.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale update err=%v", err)
	}

	if err = store.DeleteAuthorizationRole(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetAuthorizationRole(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted err=%v", err)
	}
}
