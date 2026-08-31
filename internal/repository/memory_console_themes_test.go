package repository

import (
	"bytes"
	"errors"
	"testing"
)

func TestMemoryConsoleThemePackagesAreVersionedAndCopied(t *testing.T) {
	store := NewMemoryStore()
	payload := []byte(`{"schemaVersion":1,"id":"acme-dark"}`)
	created, err := store.CreateConsoleThemePackage(t.Context(), ConsoleThemePackage{ID: "acme-dark", Payload: payload})
	if err != nil || created.Version != "1" || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	payload[0] = 'x'
	loaded, err := store.GetConsoleThemePackage(t.Context(), "acme-dark")
	if err != nil || !bytes.HasPrefix(loaded.Payload, []byte(`{"schemaVersion"`)) {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}

	replaced, err := store.ReplaceConsoleThemePackage(t.Context(), ConsoleThemePackage{ID: "acme-dark", Payload: []byte(`{"schemaVersion":1,"id":"acme-dark","name":"Acme"}`)}, "1")
	if err != nil || replaced.Version != "2" || replaced.CreatedAt != created.CreatedAt {
		t.Fatalf("replaced=%#v err=%v", replaced, err)
	}
	if _, err := store.ReplaceConsoleThemePackage(t.Context(), replaced, "1"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale replace error=%v", err)
	}
	if err := store.DeleteConsoleThemePackage(t.Context(), "acme-dark", "1"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale delete error=%v", err)
	}
	if err := store.DeleteConsoleThemePackage(t.Context(), "acme-dark", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetConsoleThemePackage(t.Context(), "acme-dark"); !errors.Is(err, ErrConsoleThemeNotFound) {
		t.Fatalf("missing error=%v", err)
	}
}

func TestMemoryConsoleThemePackageRejectsDuplicate(t *testing.T) {
	store := NewMemoryStore()
	theme := ConsoleThemePackage{ID: "acme-light", Payload: []byte(`{}`)}
	if _, err := store.CreateConsoleThemePackage(t.Context(), theme); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateConsoleThemePackage(t.Context(), theme); !errors.Is(err, ErrConsoleThemeExists) {
		t.Fatalf("duplicate error=%v", err)
	}
}
