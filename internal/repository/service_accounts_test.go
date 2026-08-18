package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryServiceAccountsUseCaseInsensitiveNamesAndAtomicCredentialState(t *testing.T) {
	store := NewMemoryStore()
	account, err := store.CreateServiceAccount(context.Background(), ServiceAccount{
		ID: "11111111-1111-4111-8111-111111111111", Name: "Release-Bot",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateServiceAccount(context.Background(), ServiceAccount{
		ID: "22222222-2222-4222-8222-222222222222", Name: "release-bot",
	}); !errors.Is(err, ErrNameExists) {
		t.Fatalf("case-insensitive duplicate error=%v want ErrNameExists", err)
	}

	expiresAt := time.Now().UTC().Add(time.Hour)
	if _, err := store.CreateServiceAccountCredential(context.Background(), APIKey{
		ID: "33333333-3333-4333-8333-333333333333", ServiceAccountID: account.ID,
		Name: "blue", SecretHash: "blue-hash", ExpiresAt: &expiresAt,
	}); err != nil {
		t.Fatalf("create active credential: %v", err)
	}
	disabled := ServiceAccountDisabled
	if _, err := store.UpdateServiceAccount(context.Background(), ServiceAccountUpdate{
		ID: account.ID, State: &disabled,
	}, account.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateServiceAccountCredential(context.Background(), APIKey{
		ID: "44444444-4444-4444-8444-444444444444", ServiceAccountID: account.ID,
		Name: "green", SecretHash: "green-hash", ExpiresAt: &expiresAt,
	}); !errors.Is(err, ErrServiceAccountDisabled) {
		t.Fatalf("disabled credential creation error=%v want ErrServiceAccountDisabled", err)
	}
}
