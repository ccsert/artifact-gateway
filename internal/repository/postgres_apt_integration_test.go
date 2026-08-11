//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func newPostgresAPTFixture(t *testing.T) (context.Context, *PostgresStore, HostedRepository) {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "apt-" + uuid.NewString(), Format: FormatAPT, Type: RepositoryTypeProxy, Endpoint: "https://deb.example.test", AllowedHosts: []string{"deb.example.test"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return ctx, store, repo
}

func TestPostgresAPTEmptyAssetPage(t *testing.T) {
	ctx, store, repo := newPostgresAPTFixture(t)
	empty, err := store.ListAPTAssets(ctx, repo.ID, "", 50, "")
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty assets=%#v err=%v", empty, err)
	}
}

func TestPostgresAPTMetadataIsMutableAndPoolIsImmutable(t *testing.T) {
	ctx, store, repo := newPostgresAPTFixture(t)
	var err error
	metadata := APTAsset{RepositoryID: repo.ID, Path: "dists/stable/InRelease", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "apt/release-a", Size: 10, ContentType: "text/plain", SourceURL: "https://deb.example.test/dists/stable/InRelease", UpstreamETag: `"a"`}
	if _, err = store.CacheAPTAsset(ctx, metadata); err != nil {
		t.Fatal(err)
	}
	metadata.Digest, metadata.ObjectKey, metadata.Size, metadata.UpstreamETag = "sha256:"+strings.Repeat("b", 64), "apt/release-b", 12, `"b"`
	if _, err = store.CacheAPTAsset(ctx, metadata); err != nil {
		t.Fatal(err)
	}
	pool := APTAsset{RepositoryID: repo.ID, Path: "pool/main/h/hello/hello_1.0_amd64.deb", Digest: "sha256:" + strings.Repeat("c", 64), ObjectKey: "apt/hello-c", Size: 20, ContentType: "application/vnd.debian.binary-package", SourceURL: "https://deb.example.test/pool/main/h/hello/hello_1.0_amd64.deb"}
	if _, err = store.CacheAPTAsset(ctx, pool); err != nil {
		t.Fatal(err)
	}
	pool.Digest, pool.ObjectKey = "sha256:"+strings.Repeat("d", 64), "apt/hello-d"
	if _, err = store.CacheAPTAsset(ctx, pool); !errors.Is(err, ErrUpstreamChanged) {
		t.Fatalf("immutable pool update err=%v", err)
	}
}

func TestPostgresAPTCapacityAndSearchProjectionIncludeZeroByteMetadata(t *testing.T) {
	ctx, store, repo := newPostgresAPTFixture(t)
	metadata := APTAsset{RepositoryID: repo.ID, Path: "dists/stable/Packages", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "apt/empty", Size: 0, ContentType: "text/plain", SourceURL: "https://deb.example.test/dists/stable/Packages"}
	if _, err := store.CacheAPTAsset(ctx, metadata); err != nil {
		t.Fatal(err)
	}
	pool := APTAsset{RepositoryID: repo.ID, Path: "pool/main/h/hello/hello_1.0_amd64.deb", Digest: "sha256:" + strings.Repeat("c", 64), ObjectKey: "apt/hello-c", Size: 20, ContentType: "application/vnd.debian.binary-package", SourceURL: "https://deb.example.test/pool/main/h/hello/hello_1.0_amd64.deb"}
	if _, err := store.CacheAPTAsset(ctx, pool); err != nil {
		t.Fatal(err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 20 || capacity.ObjectCount != 2 {
		t.Fatalf("capacity=%#v err=%v", capacity, err)
	}
	projection, err := store.SearchArtifactProjection(ctx, repo.ID, FormatAPT, ArtifactSearchQuery{Mode: ArtifactSearchByCoordinate, Value: "pool/main"}, 10, ArtifactSearchPosition{})
	if err != nil || len(projection) != 1 || projection[0].Coordinate != pool.Path || projection[0].ContentType != pool.ContentType {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
}
