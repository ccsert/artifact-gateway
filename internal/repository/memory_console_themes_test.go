package repository

import (
	"bytes"
	"errors"
	"testing"
	"time"
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

func TestMemoryConsoleThemeCatalogLockSerializesMutations(t *testing.T) {
	store := NewMemoryStore()
	release, err := store.LockConsoleThemeCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		release func()
		err     error
	}
	attempted := make(chan struct{})
	acquired := make(chan result, 1)
	go func() {
		close(attempted)
		nextRelease, lockErr := store.LockConsoleThemeCatalog(t.Context())
		acquired <- result{release: nextRelease, err: lockErr}
	}()
	<-attempted
	select {
	case <-acquired:
		t.Fatal("second catalog mutation acquired the lock before release")
	case <-time.After(100 * time.Millisecond):
	}

	release()
	select {
	case next := <-acquired:
		if next.err != nil {
			t.Fatal(next.err)
		}
		next.release()
	case <-time.After(time.Second):
		t.Fatal("second catalog mutation did not acquire the released lock")
	}
}
