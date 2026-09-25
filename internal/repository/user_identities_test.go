package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemoryUserIdentityBindingAndResolution(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	user, err := store.CreateUser(ctx, User{
		ID: "identity-user", Name: "alice", Email: "alice@example.test", Role: "member",
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
	other, err := store.CreateUser(ctx, User{ID: "identity-other", Name: "bob", Role: "member"})
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
		Provision: true, DefaultRole: "member", OccurredAt: time.Now().UTC(),
	})
	if err != nil || !wasCreated || created.Role != "member" || created.SecretHash != "" || identity.UserID != created.ID {
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
	if err != nil || wasCreated || second.ID != created.ID || second.DisplayName != "Changed" || second.Email != "changed@example.test" || refreshed.Email != "changed@example.test" {
		t.Fatalf("repeat JIT result user=%+v identity=%+v created=%v err=%v", second, refreshed, wasCreated, err)
	}

	first, err := store.CreateUser(ctx, User{ID: "email-one", Name: "email-one", Email: "shared@example.test", Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateUser(ctx, User{ID: "email-two", Name: "email-two", Email: "shared@example.test", Role: "member"}); err != nil {
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

func TestMemoryUserIdentityJITPendingRoleCanBeApprovedAndRevoked(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	provision := OIDCIdentityProvision{
		Issuer: "https://issuer.example.test", Subject: "pending-subject",
		Email: "pending@example.test", DisplayName: "Pending User",
		PreferredUsername: "pending", EmailVerified: true,
		Provision: true, DefaultRole: "none", OccurredAt: time.Now().UTC(),
	}
	user, identity, created, err := store.ResolveOIDCIdentity(ctx, provision)
	if err != nil || !created || user.Role != "none" || user.SecretHash != "" || identity.UserID != user.ID {
		t.Fatalf("pending JIT user=%+v identity=%+v created=%v err=%v", user, identity, created, err)
	}
	page, err := store.ListUsers(ctx, UserListQuery{Role: "none", Limit: 10})
	if err != nil || page.Total != 1 || page.Items[0].ID != user.ID {
		t.Fatalf("pending user list=%+v err=%v", page, err)
	}
	member := "member"
	user, err = store.UpdateUser(ctx, UserUpdate{ID: user.ID, Role: &member}, user.Version)
	if err != nil || user.Role != "member" {
		t.Fatalf("approve user=%+v err=%v", user, err)
	}
	provision.Email = "new@example.test"
	same, refreshed, created, err := store.ResolveOIDCIdentity(ctx, provision)
	if err != nil || created || same.ID != user.ID || same.Role != "member" || same.Email != provision.Email || refreshed.Email != provision.Email {
		t.Fatalf("repeat login user=%+v identity=%+v created=%v err=%v", same, refreshed, created, err)
	}
	none := "none"
	revoked, err := store.UpdateUser(ctx, UserUpdate{ID: user.ID, Role: &none}, same.Version)
	if err != nil || revoked.Role != "none" {
		t.Fatalf("revoke user=%+v err=%v", revoked, err)
	}
	adminProvision := provision
	adminProvision.Subject = "admin-subject"
	adminProvision.PreferredUsername = "admin"
	adminProvision.Role = "admin"
	admin, _, created, err := store.ResolveOIDCIdentity(ctx, adminProvision)
	if err != nil || !created || admin.Role != "admin" {
		t.Fatalf("admin subject bootstrap user=%+v created=%v err=%v", admin, created, err)
	}
}

func TestMemoryUserIdentityJITRejectsRemovedLegacyLevels(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// A mapped role or a configured default that no longer names a level is
	// ignored in favour of the member level, so a stale mapping cannot hand out
	// repository reach; a surviving level is still honored.
	for _, tc := range []struct {
		name     string
		mapped   string
		fallback string
		want     string
	}{
		{name: "removed mapped role falls back to the configured default", mapped: "writer", fallback: "none", want: "none"},
		{name: "removed default falls back to member", fallback: "reader", want: "member"},
		{name: "nothing recognized falls back to member", mapped: "unknown", fallback: "unknown-level", want: "member"},
		{name: "member mapping wins", mapped: "member", fallback: "none", want: "member"},
		{name: "admin mapping wins", mapped: "admin", want: "admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := strings.ReplaceAll(tc.name, " ", "-")
			user, _, created, err := store.ResolveOIDCIdentity(ctx, OIDCIdentityProvision{
				Issuer: "https://issuer.example.test", Subject: subject,
				Email: subject + "@example.test", PreferredUsername: subject,
				Role: tc.mapped, DefaultRole: tc.fallback, Provision: true, OccurredAt: time.Now().UTC(),
			})
			if err != nil || !created {
				t.Fatalf("provision user=%+v created=%v err=%v", user, created, err)
			}
			if user.Role != tc.want {
				t.Fatalf("role=%q want=%q", user.Role, tc.want)
			}
		})
	}
}
