//go:build integration

package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRustFSConanNativeManifestOnlyReplay(t *testing.T) {
	databaseURL, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	access, secret := os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY")
	if databaseURL == "" || endpoint == "" || access == "" || secret == "" {
		if os.Getenv("CONAN_PERSISTENCE_REQUIRED") == "1" {
			t.Fatal("PostgreSQL and S3 are required")
		}
		t.Skip("PostgreSQL and S3 are required")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	bucket := "conan-retry-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	objects, err := objectstore.NewRustFSStore(endpoint, access, secret, bucket)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = objects.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	exerciseConanNativeManifestOnlyReplay(t, conanClientBackend{store, store, objects})
}
