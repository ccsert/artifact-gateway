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
