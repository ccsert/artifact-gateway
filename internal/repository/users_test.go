package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryUserProfileFilteringAndAuthenticationLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	created, err := store.CreateUser(ctx, User{
		ID:          "alice-id",
		Name:        "alice",
		DisplayName: "Alice Example",
		Email:       "alice@example.test",
		Description: "build account",
		SecretHash:  "hash",
		Role:        "reader",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.SessionVersion != 1 || created.PasswordChangedAt == nil {
		t.Fatalf("created user lifecycle fields=%+v", created)
	}
	caseInsensitive, err := store.GetUserByName(ctx, "ALICE")
	if err != nil || caseInsensitive.ID != created.ID {
		t.Fatalf("case-insensitive lookup=%+v err=%v", caseInsensitive, err)
	}
	if _, err := store.CreateUser(ctx, User{ID: "bob-id", Name: "bob", DisplayName: "Bob", Role: "writer"}); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListUsers(ctx, UserListQuery{Search: "alice", Role: "reader", State: UserActive, Limit: 10})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Email != "alice@example.test" {
		t.Fatalf("filtered users page=%+v err=%v", page, err)
	}

	displayName, email, description := "Alice Builder", "alice@new.example", "updated"
	updated, err := store.UpdateUser(ctx, UserUpdate{ID: created.ID, DisplayName: &displayName, Email: &email, Description: &description}, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Alice Builder" || updated.Email != "alice@new.example" || updated.Description != "updated" {
		t.Fatalf("profile was not updated: %+v", updated)
	}

	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	for attempt := 1; attempt <= 3; attempt++ {
		updated, err = store.RecordUserLoginFailure(ctx, created.ID, now, 3, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if updated.FailedLoginAttempts != attempt {
			t.Fatalf("attempt %d state=%+v", attempt, updated)
		}
	}
	if updated.LockedUntil == nil || !updated.LockedUntil.After(now) {
		t.Fatalf("user was not locked after failed attempts: %+v", updated)
	}
	if _, err := store.RecordUserLoginSuccess(ctx, created.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	updated, err = store.GetUser(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FailedLoginAttempts != 0 || updated.LockedUntil != nil || updated.LastLoginAt == nil {
		t.Fatalf("successful login did not clear lock state: %+v", updated)
	}
	updated, err = store.RecordUserLoginFailure(ctx, created.ID, now.Add(2*time.Second), 1, time.Minute)
	if err != nil || updated.LockedUntil == nil {
		t.Fatalf("failed login before reset=%+v err=%v", updated, err)
	}

	passwordUpdated, err := store.UpdateUserPassword(ctx, created.ID, "new-hash", updated.Version, true)
	if err != nil {
		t.Fatal(err)
	}
	if passwordUpdated.SecretHash != "new-hash" || !passwordUpdated.MustChangePassword || passwordUpdated.SessionVersion != 2 || passwordUpdated.PasswordChangedAt == nil || passwordUpdated.FailedLoginAttempts != 0 || passwordUpdated.LockedUntil != nil {
		t.Fatalf("password lifecycle fields=%+v", passwordUpdated)
	}
	revoked, err := store.RevokeUserSessions(ctx, created.ID, passwordUpdated.Version)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.SessionVersion != 3 {
		t.Fatalf("session version=%d want=3", revoked.SessionVersion)
	}
}

func TestMemoryUserStoreProtectsLastActiveAdministrator(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	first, err := store.CreateUser(ctx, User{ID: "admin-1", Name: "admin-one", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	reader := "reader"
	if _, err = store.UpdateUser(ctx, UserUpdate{ID: first.ID, Role: &reader}, first.Version); !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("demote last admin error=%v want=%v", err, ErrLastActiveAdmin)
	}
	if err = store.DeleteUser(ctx, first.ID); !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("delete last admin error=%v want=%v", err, ErrLastActiveAdmin)
	}
	second, err := store.CreateUser(ctx, User{ID: "admin-2", Name: "admin-two", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateUser(ctx, UserUpdate{ID: first.ID, Role: &reader}, first.Version); err != nil {
		t.Fatalf("demote with second admin: %v", err)
	}
	if err = store.DeleteUser(ctx, second.ID); !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("delete remaining admin error=%v want=%v", err, ErrLastActiveAdmin)
	}
}
