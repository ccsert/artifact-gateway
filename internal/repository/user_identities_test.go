package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryUserIdentityBindingAndResolution(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	user, err := store.CreateUser(ctx, User{
		ID: "identity-user", Name: "alice", Email: "alice@example.test", Role: "writer",
		SecretHash: "local-hash",
	})
	if err != nil {
		t.Fatal(err)
	}

	identity, err := store.CreateUserIdentity(ctx, UserIdentity{
		ID: "identity-1", UserID: user.ID, Kind: UserIdentityOIDC,
		Issuer: "https://issuer.example.test/", Subject: "  subject-1  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Issuer != "https://issuer.example.test" || identity.Subject != "subject-1" || identity.CreatedAt.IsZero() {
		t.Fatalf("normalized identity=%+v", identity)
	}

	if _, err := store.CreateUserIdentity(ctx, UserIdentity{
		UserID: user.ID, Kind: UserIdentityOIDC,
		Issuer: "https://issuer.example.test", Subject: "subject-2",
	}); !errors.Is(err, ErrIdentityExists) {
		t.Fatalf("second identity for issuer error=%v want=%v", err, ErrIdentityExists)
	}
	other, err := store.CreateUser(ctx, User{ID: "identity-other", Name: "bob", Role: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUserIdentity(ctx, UserIdentity{
		UserID: other.ID, Kind: UserIdentityOIDC,
		Issuer: "https://issuer.example.test", Subject: "subject-1",
	}); !errors.Is(err, ErrIdentityExists) {
		t.Fatalf("duplicate provider subject error=%v want=%v", err, ErrIdentityExists)
	}

	occurredAt := time.Date(2026, time.August, 10, 8, 30, 0, 0, time.UTC)
	resolved, refreshed, created, err := store.ResolveOIDCIdentity(ctx, OIDCIdentityProvision{
		Issuer: "https://issuer.example.test/", Subject: "subject-1", Email: "alice@new.example",
		DisplayName: "Alice OIDC", EmailVerified: true, OccurredAt: occurredAt,
	})
	if err != nil || created || resolved.ID != user.ID {
		t.Fatalf("bound identity resolution user=%+v identity=%+v created=%v err=%v", resolved, refreshed, created, err)
	}
	if refreshed.Email != "alice@new.example" || refreshed.DisplayName != "Alice OIDC" || !refreshed.EmailVerified || refreshed.LastLoginAt == nil || !refreshed.LastLoginAt.Equal(occurredAt) {
		t.Fatalf("refreshed identity=%+v", refreshed)
	}
	loaded, loadedIdentity, err := store.GetUserByOIDCIdentity(ctx, "https://issuer.example.test", "subject-1")
	if err != nil || loaded.ID != user.ID || loadedIdentity.ID != identity.ID {
		t.Fatalf("loaded mapping user=%+v identity=%+v err=%v", loaded, loadedIdentity, err)
	}

	if err := store.DeleteUserIdentity(ctx, user.ID, identity.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.GetUserByOIDCIdentity(ctx, identity.Issuer, identity.Subject); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted identity lookup error=%v want=%v", err, ErrNotFound)
	}
}

func TestMemoryUserIdentityJITProvisioningAndEmailSafety(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	if _, _, _, err := store.ResolveOIDCIdentity(ctx, OIDCIdentityProvision{
		Issuer: "https://issuer.example.test", Subject: "unlinked", Email: "new@example.test",
		Provision: false,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled provisioning error=%v want=%v", err, ErrNotFound)
	}

	created, identity, wasCreated, err := store.ResolveOIDCIdentity(ctx, OIDCIdentityProvision{
		Issuer: "https://issuer.example.test", Subject: "jit-subject", Email: "new@example.test",
		DisplayName: "JIT User", PreferredUsername: "jit-user", EmailVerified: true,
		Provision: true, DefaultRole: "writer", OccurredAt: time.Now().UTC(),
	})
	if err != nil || !wasCreated || created.Role != "writer" || created.SecretHash != "" || identity.UserID != created.ID {
		t.Fatalf("JIT result user=%+v identity=%+v created=%v err=%v", created, identity, wasCreated, err)
	}
	if created.PasswordChangedAt != nil || created.LastLoginAt == nil || identity.LastLoginAt == nil || !created.LastLoginAt.Equal(*identity.LastLoginAt) {
		t.Fatalf("JIT password/login lifecycle user=%+v identity=%+v", created, identity)
	}

	second, refreshed, wasCreated, err := store.ResolveOIDCIdentity(ctx, OIDCIdentityProvision{
		Issuer: identity.Issuer, Subject: identity.Subject, Email: "changed@example.test",
		DisplayName: "Changed", EmailVerified: true, Provision: true,
		OccurredAt: time.Now().UTC(),
	})
	if err != nil || wasCreated || second.ID != created.ID || refreshed.Email != "changed@example.test" {
		t.Fatalf("repeat JIT result user=%+v identity=%+v created=%v err=%v", second, refreshed, wasCreated, err)
	}

	first, err := store.CreateUser(ctx, User{ID: "email-one", Name: "email-one", Email: "shared@example.test", Role: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser(ctx, User{ID: "email-two", Name: "email-two", Email: "shared@example.test", Role: "reader"}); err != nil {
		// Memory users intentionally allow duplicate email values; ambiguity is
		// rejected by the identity resolver instead of by account creation.
		t.Fatal(err)
	}
	if _, _, _, err := store.ResolveOIDCIdentity(ctx, OIDCIdentityProvision{
		Issuer: "https://issuer.example.test", Subject: "ambiguous", Email: first.Email,
		EmailVerified: true, Provision: true, MatchEmail: true,
	}); !errors.Is(err, ErrIdentityAmbiguous) {
		t.Fatalf("ambiguous email error=%v want=%v", err, ErrIdentityAmbiguous)
	}
}
