//go:build integration

package app

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresAPIKeyRevocation(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	token := "agk_postgres-test-token-" + uuid.NewString()
	expiresAt := time.Now().UTC().Add(time.Hour)
	key, err := store.CreateAPIKey(context.Background(), repository.APIKey{ID: uuid.NewString(), Name: "integration", SecretHash: authorization.HashAPIKey(token), Roles: []string{"admin"}, ExpiresAt: &expiresAt})
	if err != nil {
		t.Fatal(err)
	}
	if found, err := store.FindActiveAPIKeyByHash(context.Background(), authorization.HashAPIKey(token)); err != nil || found.ID != key.ID || found.LastUsedAt == nil {
		t.Fatalf("find active key=%#v err=%v", found, err)
	}
	authenticator := authorization.Authenticator{APIKeys: store}
	if principal, ok := authenticator.Authenticate("Bearer " + token); !ok || !principal.Admin {
		t.Fatalf("principal=%#v authenticated=%t", principal, ok)
	}
	if _, err := store.RevokeAPIKey(context.Background(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := authenticator.Authenticate("Bearer " + token); ok {
		t.Fatal("revoked PostgreSQL API key authenticated")
	}

	expiredToken := "agk_postgres-expired-" + uuid.NewString()
	past := time.Now().UTC().Add(-time.Minute)
	if _, err := store.CreateAPIKey(context.Background(), repository.APIKey{ID: uuid.NewString(), Name: "expired", SecretHash: authorization.HashAPIKey(expiredToken), Roles: []string{"reader"}, ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindActiveAPIKeyByHash(context.Background(), authorization.HashAPIKey(expiredToken)); err != repository.ErrNotFound {
		t.Fatalf("expired key lookup error=%v want ErrNotFound", err)
	}
}

func TestPostgresServiceAccountCredentialRotationAndDisable(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	account, err := store.CreateServiceAccount(context.Background(), repository.ServiceAccount{
		ID: uuid.NewString(), Name: "postgres-ci-" + uuid.NewString(), Description: "integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "agc_postgres-service-account-" + uuid.NewString()
	expiresAt := time.Now().UTC().Add(time.Hour)
	credential, err := store.CreateServiceAccountCredential(context.Background(), repository.APIKey{
		ID: uuid.NewString(), ServiceAccountID: account.ID, Name: "blue",
		SecretHash: authorization.HashAPIKey(token), Roles: []string{}, ExpiresAt: &expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := authorization.Authenticator{APIKeys: store, ServiceAccounts: store}
	principal, ok := authenticator.Authenticate("Bearer " + token)
	if !ok || principal.Actor != "service-account:"+account.ID || principal.AuthenticationKind != authorization.AuthenticationServiceAccountCredential {
		t.Fatalf("principal=%#v authenticated=%t", principal, ok)
	}
	standalone, err := store.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range standalone {
		if key.ID == credential.ID {
			t.Fatalf("service-account credential leaked into standalone keys: %#v", key)
		}
	}
	if _, err := store.RevokeAPIKey(context.Background(), credential.ID); err != repository.ErrNotFound {
		t.Fatalf("generic revoke error=%v want ErrNotFound", err)
	}

	disabled := repository.ServiceAccountDisabled
	if _, err := store.UpdateServiceAccount(context.Background(), repository.ServiceAccountUpdate{ID: account.ID, State: &disabled}, account.Version); err != nil {
		t.Fatal(err)
	}
	if _, ok := authenticator.Authenticate("Bearer " + token); ok {
		t.Fatal("disabled PostgreSQL service account credential authenticated")
	}
	if _, err := store.CreateServiceAccountCredential(context.Background(), repository.APIKey{
		ID: uuid.NewString(), ServiceAccountID: account.ID, Name: "green",
		SecretHash: authorization.HashAPIKey("must-not-issue"), Roles: []string{}, ExpiresAt: &expiresAt,
	}); !errors.Is(err, repository.ErrServiceAccountDisabled) {
		t.Fatalf("disabled credential creation error=%v want ErrServiceAccountDisabled", err)
	}
	credentials, err := store.ListServiceAccountCredentials(context.Background(), account.ID, 200, "")
	if err != nil || len(credentials) != 1 || credentials[0].LastUsedAt == nil {
		t.Fatalf("credentials=%#v err=%v", credentials, err)
	}
}
