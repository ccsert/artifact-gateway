//go:build integration

package repository

import (
	"os"
	"testing"
	"time"
)

func TestPostgresConanNativePublication(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exerciseConanNativePublication(t, store, func(id string) {
		if _, err := store.db.Exec(`UPDATE native_conan_publish_sessions SET expires_at=$2 WHERE id=$1`, id, time.Now().Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPostgresConanNativeReplaySnapshot(t *testing.T) {
	store := openPostgresConanManagedContractStore(t)
	exerciseConanNativeReplaySnapshot(t, store, func(t *testing.T, id string) {
		t.Helper()
		if _, err := store.db.Exec(`UPDATE native_conan_publish_sessions SET expires_at=$2 WHERE id=$1`, id, time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}, func(t *testing.T, key string, collecting bool) {
		t.Helper()
		if _, err := store.db.Exec(`UPDATE native_conan_object_intents SET collecting_at=CASE WHEN $2 THEN now() ELSE collecting_at END,collected_at=CASE WHEN $2 THEN collected_at ELSE now() END WHERE object_key=$1`, key, collecting); err != nil {
			t.Fatal(err)
		}
	})
}
