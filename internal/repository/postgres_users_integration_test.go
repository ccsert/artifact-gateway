//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresUserGovernanceLifecycle(t *testing.T) {
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
	id := uuid.NewString()
	name := "integration-user-" + strings.ToLower(uuid.NewString())
	created, err := store.CreateUser(ctx, User{
		ID: id, Name: name, DisplayName: "Integration User",
		Email: "integration@example.test", Description: "user governance integration test",
		SecretHash: "test-hash", Role: "writer", MustChangePassword: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteUser(ctx, id) }()
	if created.SessionVersion != 1 || created.PasswordChangedAt == nil || !created.MustChangePassword {
		t.Fatalf("created user defaults=%+v", created)
	}

	_, err = store.CreateUser(ctx, User{ID: uuid.NewString(), Name: strings.ToUpper(name), SecretHash: "duplicate", Role: "reader"})
	if !errors.Is(err, ErrNameExists) {
		t.Fatalf("case-insensitive duplicate error=%v want=%v", err, ErrNameExists)
	}

	page, err := store.ListUsers(ctx, UserListQuery{Search: "INTEGRATION@EXAMPLE.TEST", Role: "writer", State: UserActive, Limit: 20})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != id {
		t.Fatalf("filtered users=%+v err=%v", page, err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	failed, err := store.RecordUserLoginFailure(ctx, id, now, 2, 30*time.Minute)
	if err != nil || failed.FailedLoginAttempts != 1 || failed.LockedUntil != nil {
		t.Fatalf("first failure=%+v err=%v", failed, err)
	}
	locked, err := store.RecordUserLoginFailure(ctx, id, now.Add(time.Second), 2, 30*time.Minute)
	if err != nil || locked.FailedLoginAttempts != 2 || locked.LockedUntil == nil {
		t.Fatalf("locked user=%+v err=%v", locked, err)
	}
	signedIn, err := store.RecordUserLoginSuccess(ctx, id, now.Add(2*time.Second))
	if err != nil || signedIn.FailedLoginAttempts != 0 || signedIn.LockedUntil != nil || signedIn.LastLoginAt == nil {
		t.Fatalf("successful login=%+v err=%v", signedIn, err)
	}

	displayName, description := "Updated User", ""
	updated, err := store.UpdateUser(ctx, UserUpdate{ID: id, DisplayName: &displayName, Description: &description}, signedIn.Version)
	if err != nil || updated.DisplayName != displayName || updated.Description != "" {
		t.Fatalf("updated profile=%+v err=%v", updated, err)
	}
	passwordUpdated, err := store.UpdateUserPassword(ctx, id, "new-hash", updated.Version, false)
	if err != nil || passwordUpdated.SessionVersion != created.SessionVersion+1 || passwordUpdated.MustChangePassword {
		t.Fatalf("password update=%+v err=%v", passwordUpdated, err)
	}
	revoked, err := store.RevokeUserSessions(ctx, id, passwordUpdated.Version)
	if err != nil || revoked.SessionVersion != passwordUpdated.SessionVersion+1 {
		t.Fatalf("session revoke=%+v err=%v", revoked, err)
	}
}

func TestPostgresUserIdentityBindingAndJITProvisioning(t *testing.T) {
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
	suffix := strings.ToLower(uuid.NewString())
	issuer := "https://issuer.example.test/" + suffix
	local, err := store.CreateUser(ctx, User{
		ID: uuid.NewString(), Name: "identity-local-" + suffix,
		Email: "identity-" + suffix + "@example.test", Role: "reader",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteUser(ctx, local.ID) }()
	if local.PasswordChangedAt != nil || local.SecretHash != "" {
		t.Fatalf("external-only user lifecycle=%+v", local)
	}

	linked, err := store.CreateUserIdentity(ctx, UserIdentity{
		ID: uuid.NewString(), UserID: local.ID, Kind: UserIdentityOIDC,
		Issuer: issuer + "/", Subject: "provider-subject",
	})
	if err != nil {
		t.Fatal(err)
	}
	if linked.Issuer != issuer {
		t.Fatalf("normalized issuer=%q want=%q", linked.Issuer, issuer)
	}
	listed, err := store.ListUserIdentities(ctx, local.ID)
	if err != nil || len(listed) != 1 || listed[0].ID != linked.ID {
		t.Fatalf("listed identities=%+v err=%v", listed, err)
	}

	occurredAt := time.Now().UTC().Truncate(time.Microsecond)
	resolved, refreshed, created, err := store.ResolveOIDCIdentity(ctx, OIDCIdentityProvision{
		Issuer: issuer, Subject: linked.Subject, Email: "refreshed@example.test",
		DisplayName: "Refreshed Identity", EmailVerified: true, OccurredAt: occurredAt,
	})
	if err != nil || created || resolved.ID != local.ID || refreshed.Email != "refreshed@example.test" || refreshed.LastLoginAt == nil {
		t.Fatalf("resolved user=%+v identity=%+v created=%v err=%v", resolved, refreshed, created, err)
	}

	jitUser, jitIdentity, created, err := store.ResolveOIDCIdentity(ctx, OIDCIdentityProvision{
		Issuer: issuer, Subject: "jit-subject", Email: "jit-" + suffix + "@example.test",
		DisplayName: "JIT Identity", PreferredUsername: "jit-" + suffix,
		EmailVerified: true, Provision: true, DefaultRole: "writer", OccurredAt: occurredAt,
	})
	if err != nil || !created || jitUser.Role != "writer" || jitUser.PasswordChangedAt != nil || jitIdentity.UserID != jitUser.ID {
		t.Fatalf("JIT user=%+v identity=%+v created=%v err=%v", jitUser, jitIdentity, created, err)
	}
	defer func() { _ = store.DeleteUser(ctx, jitUser.ID) }()
}
