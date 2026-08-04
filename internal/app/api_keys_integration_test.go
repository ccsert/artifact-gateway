//go:build integration

package app

import (
	"context"
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
