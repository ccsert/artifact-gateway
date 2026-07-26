package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryRawUploadPublishesOnlyOnComplete(t *testing.T) {
	store := NewMemoryStore()
	upload := RawUpload{ID: "upload", RepositoryID: "repo", Path: "releases/app.bin", ObjectKey: "native/raw/uploads/upload", State: "open", ExpiresAt: time.Now().Add(time.Hour)}
	if _, err := store.CreateRawUpload(context.Background(), upload); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRawUpload(context.Background(), upload.ID, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetRawAsset(context.Background(), upload.RepositoryID, upload.Path); !errors.Is(err, ErrNotFound) {
		t.Fatalf("asset visible before completion: %v", err)
	}
	asset, err := store.CompleteRawUpload(context.Background(), upload.ID, RawAsset{RepositoryID: upload.RepositoryID, Path: upload.Path, Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ObjectKey: "native/raw/sha256/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 4, ContentType: "application/octet-stream"})
	if err != nil {
		t.Fatal(err)
	}
	if asset.Path != upload.Path {
		t.Fatalf("asset=%#v", asset)
	}
	if _, err := store.GetRawAsset(context.Background(), upload.RepositoryID, upload.Path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRawUpload(context.Background(), upload.ID, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("completed upload remained writable: %v", err)
	}
}
