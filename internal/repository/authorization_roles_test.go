package repository

import (
	"context"
	"testing"
)

func TestMemoryAuthorizationRoleLifecycleAndCopies(t *testing.T) {
	store := NewMemoryStore()
	created, err := store.CreateAuthorizationRole(context.Background(), AuthorizationRole{Name: " Release readers ", Description: "read releases", Scopes: []string{"repositories:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Name != "Release readers" || created.Version != "1" {
		t.Fatalf("created = %#v", created)
	}
	created.Scopes[0] = "repositories:admin"
	loaded, err := store.GetAuthorizationRole(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Scopes[0] != "repositories:read" {
		t.Fatalf("stored scopes mutated through caller: %#v", loaded.Scopes)
	}
	updated, err := store.UpdateAuthorizationRole(context.Background(), AuthorizationRole{ID: created.ID, Name: "Release consumers", Scopes: []string{"repositories:read", "repositories:intelligence"}}, "1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != "2" || len(updated.Scopes) != 2 {
		t.Fatalf("updated = %#v", updated)
	}
	if _, err := store.UpdateAuthorizationRole(context.Background(), updated, "1"); err != ErrVersionConflict {
		t.Fatalf("stale update err = %v", err)
	}
	if err := store.DeleteAuthorizationRole(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAuthorizationRole(context.Background(), created.ID); err != ErrNotFound {
		t.Fatalf("get deleted err = %v", err)
	}
}

func TestMemoryAuthorizationRoleNamesAreCaseInsensitive(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.CreateAuthorizationRole(context.Background(), AuthorizationRole{Name: "Release readers", Scopes: []string{"repositories:read"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAuthorizationRole(context.Background(), AuthorizationRole{Name: " release READERS ", Scopes: []string{"repositories:read"}}); err != ErrAuthorizationRoleNameExists {
		t.Fatalf("duplicate err = %v", err)
	}
}
