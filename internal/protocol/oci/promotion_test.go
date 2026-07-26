package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativePromotionCopiesManifestAndMountsSourceBlob(t *testing.T) {
	ctx := context.Background()
	store, objects := repository.NewMemoryStore(), objectstore.NewMemoryStore()
	body := []byte("blob")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := objects.Put(ctx, "blob", body); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateOCIUpload(ctx, repository.OCIUpload{ID: "upload", RepositoryID: "source", State: "open", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: "source", ObjectKey: "blob", Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteOCIUpload(ctx, "upload", repository.OCIBlob{Digest: digest, ObjectKey: "blob", Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"schemaVersion":2,"config":{"digest":"` + digest + `"}}`)
	manifestSum := sha256.Sum256(manifest)
	manifestDigest := "sha256:" + hex.EncodeToString(manifestSum[:])
	if err := objects.Put(ctx, "source-manifest", manifest); err != nil {
		t.Fatal(err)
	}
	if err := store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: "source", ObjectKey: "source-manifest", Digest: manifestDigest, Size: int64(len(manifest))}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: "source", Name: "team/widget", Digest: manifestDigest, ObjectKey: "source-manifest", Size: int64(len(manifest))}, manifestDigest); err != nil {
		t.Fatal(err)
	}
	worker := NativePromotion{Store: store, Objects: objects}
	if _, _, err := worker.Enqueue(ctx, "target", "promotion", PromotionPayload{SourceRepositoryID: "source", Name: "team/widget", Digest: manifestDigest}); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	target, err := store.GetOCIManifest(ctx, "target", "team/widget", manifestDigest)
	if err != nil || target.ObjectKey == "source-manifest" {
		t.Fatalf("target=%#v err=%v", target, err)
	}
	if _, err = store.GetOCIBlob(ctx, "target", digest); err != nil {
		t.Fatalf("mounted blob: %v", err)
	}
}
