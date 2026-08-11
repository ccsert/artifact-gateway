package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMemoryAPTStoreReturnsAnEmptyAssetPage(t *testing.T) {
	items, err := NewMemoryStore().ListAPTAssets(context.Background(), "apt", "", 50, "")
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%#v err=%v", items, err)
	}
}

func TestMemoryAPTStoreUpdatesMutableMetadata(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	_, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "apt", Name: "apt", Format: FormatAPT, Type: RepositoryTypeProxy})
	if err != nil {
		t.Fatal(err)
	}
	metadata := APTAsset{RepositoryID: "apt", Path: "dists/stable/InRelease", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "apt/release-a", Size: 10, SourceURL: "https://example.test/dists/stable/InRelease", UpstreamETag: `"a"`}
	if _, err = store.CacheAPTAsset(ctx, metadata); err != nil {
		t.Fatal(err)
	}
	metadata.Digest, metadata.ObjectKey, metadata.Size, metadata.UpstreamETag = "sha256:"+strings.Repeat("b", 64), "apt/release-b", 12, `"b"`
	updated, err := store.CacheAPTAsset(ctx, metadata)
	if err != nil || updated.Digest != metadata.Digest || updated.UpstreamETag != `"b"` {
		t.Fatalf("mutable metadata=%#v err=%v", updated, err)
	}
}

func TestMemoryAPTStoreProtectsPoolAndByHashAssets(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, path := range []string{"pool/main/h/hello/hello_1.0_amd64.deb", "dists/stable/main/binary-amd64/by-hash/SHA256/release"} {
		asset := APTAsset{RepositoryID: "apt", Path: path, Digest: "sha256:" + strings.Repeat("c", 64), ObjectKey: "apt/object-" + path, Size: 20, SourceURL: "https://example.test/" + path}
		if _, err := store.CacheAPTAsset(ctx, asset); err != nil {
			t.Fatal(err)
		}
		asset.Digest, asset.ObjectKey = "sha256:"+strings.Repeat("d", 64), "apt/changed"
		if _, err := store.CacheAPTAsset(ctx, asset); !errors.Is(err, ErrUpstreamChanged) {
			t.Fatalf("immutable %s update err=%v", path, err)
		}
	}
}

func TestMemoryAPTStoreCountsZeroByteAssets(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "apt", Name: "apt", Format: FormatAPT, Type: RepositoryTypeProxy}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CacheAPTAsset(ctx, APTAsset{RepositoryID: "apt", Path: "dists/stable/Packages", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "apt/empty", Size: 0, SourceURL: "https://example.test/dists/stable/Packages"}); err != nil {
		t.Fatal(err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, "apt")
	if err != nil || capacity.UsedBytes != 0 || capacity.ObjectCount != 1 {
		t.Fatalf("capacity=%#v err=%v", capacity, err)
	}
}
