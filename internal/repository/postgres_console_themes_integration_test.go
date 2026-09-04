//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPostgresConsoleThemesAreSharedAndVersioned(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	first, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPostgresStore(databaseURL)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = second.Close()
		_ = first.Close()
	})

	themeID := fmt.Sprintf("managed-theme-%d", time.Now().UnixNano())
	created, err := first.CreateConsoleThemePackage(t.Context(), ConsoleThemePackage{
		ID:      themeID,
		Payload: []byte(`{"schemaVersion":1}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		current, loadErr := first.GetConsoleThemePackage(context.Background(), themeID)
		if loadErr == nil {
			_ = first.DeleteConsoleThemePackage(context.Background(), themeID, current.Version)
		}
	})
	if created.Version != "1" || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created=%#v", created)
	}

	shared, err := second.GetConsoleThemePackage(t.Context(), themeID)
	if err != nil || string(shared.Payload) != `{"schemaVersion": 1}` {
		t.Fatalf("shared=%#v err=%v", shared, err)
	}
	replaced, err := first.ReplaceConsoleThemePackage(t.Context(), ConsoleThemePackage{
		ID:      themeID,
		Payload: []byte(`{"schemaVersion":1,"name":"updated"}`),
	}, shared.Version)
	if err != nil || replaced.Version != "2" {
		t.Fatalf("replaced=%#v err=%v", replaced, err)
	}
	if _, err = second.ReplaceConsoleThemePackage(t.Context(), ConsoleThemePackage{
		ID:      themeID,
		Payload: []byte(`{"schemaVersion":1}`),
	}, shared.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale replace error=%v", err)
	}
	if err = second.DeleteConsoleThemePackage(t.Context(), themeID, replaced.Version); err != nil {
		t.Fatal(err)
	}
	if _, err = first.GetConsoleThemePackage(t.Context(), themeID); !errors.Is(err, ErrConsoleThemeNotFound) {
		t.Fatalf("deleted theme error=%v", err)
	}
}

func TestPostgresConsoleThemeCatalogLockIsSharedAcrossStores(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	first, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPostgresStore(databaseURL)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = second.Close()
		_ = first.Close()
	})

	release, err := first.LockConsoleThemeCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	type result struct {
		release func()
		err     error
	}
	attempted := make(chan struct{})
	acquired := make(chan result, 1)
	go func() {
		close(attempted)
		nextRelease, lockErr := second.LockConsoleThemeCatalog(t.Context())
		acquired <- result{release: nextRelease, err: lockErr}
	}()
	<-attempted
	select {
	case <-acquired:
		t.Fatal("second Gateway store acquired the catalog lock before release")
	case <-time.After(100 * time.Millisecond):
	}

	release()
	released = true
	select {
	case next := <-acquired:
		if next.err != nil {
			t.Fatal(next.err)
		}
		next.release()
	case <-time.After(time.Second):
		t.Fatal("second Gateway store did not acquire the released catalog lock")
	}
}
