package secrets

import (
	"errors"
	"testing"
)

func TestSealAndOpenBindsCiphertextToPurpose(t *testing.T) {
	t.Setenv(KeyEnv, "0123456789abcdef0123456789abcdef")
	sealed, err := Seal("oidc.client-secret", "client-secret")
	if err != nil {
		t.Fatal(err)
	}
	if sealed == "client-secret" {
		t.Fatal("plaintext was not encrypted")
	}
	opened, err := Open("oidc.client-secret", sealed)
	if err != nil || opened != "client-secret" {
		t.Fatalf("opened=%q err=%v", opened, err)
	}
	if _, err := Open("another-purpose", sealed); err == nil {
		t.Fatal("ciphertext must not be reusable for another setting")
	}
}

func TestSealRequiresEncryptionKey(t *testing.T) {
	t.Setenv(KeyEnv, "")
	if _, err := Seal("oidc.client-secret", "client-secret"); !errors.Is(err, ErrKeyNotConfigured) {
		t.Fatalf("err=%v", err)
	}
}

func TestSealRejectsInvalidEncryptionKey(t *testing.T) {
	t.Setenv(KeyEnv, "too-short")
	if _, err := Seal("oidc.client-secret", "client-secret"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("err=%v", err)
	}
}
