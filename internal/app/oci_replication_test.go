package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestOCIReplicationCopiesManifestToTargetOwnedKey(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	body := []byte(`{"schemaVersion":2}`)
	digest := digestOCIReplicationBody(body)
	sourceKey := "native/oci/manifests/source/team%2Fwidget/" + digest[7:]
	targetKey := ociReplicationTargetObjectKey("target", "team/widget", digest)
	if err := objects.PutVerifiedReader(ctx, sourceKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	if err := store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: "source", ObjectKey: sourceKey, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: "source", Name: "team/widget", Digest: digest, ObjectKey: sourceKey, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(body))}, digest); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{ID: "plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatOCI, IdempotencyKey: "replicate"}
	checkpoint := repository.ReplicationCheckpoint{SourceObjectKey: sourceKey, ObjectKey: targetKey, Digest: digest, Size: int64(len(body))}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{checkpoint}); err != nil {
		t.Fatal(err)
	}
	if err := (OCIReplication{Store: store, Source: objects, Destination: objects, ChunkBytes: 3}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	target, err := store.GetOCIManifest(ctx, "target", "team/widget", digest)
	if err != nil || target.ObjectKey != targetKey || target.ObjectKey == sourceKey {
		t.Fatalf("target=%#v err=%v", target, err)
	}
	if got, err := objects.Get(ctx, targetKey); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("target object=%q err=%v", got, err)
	}
}

func TestOCIReplicationRejectsSourceManifestRemovedAfterCopy(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	body := []byte(`{"schemaVersion":2}`)
	digest := digestOCIReplicationBody(body)
	sourceKey := "source-manifest"
	targetKey := "target-manifest"
	if err := objects.PutVerifiedReader(ctx, sourceKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	if err := store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{RepositoryID: "source", ObjectKey: sourceKey, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: "source", Name: "widget", Digest: digest, ObjectKey: sourceKey, Size: int64(len(body))}, digest); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteOCIManifest(ctx, "source", "widget", digest); err != nil {
		t.Fatal(err)
	}
	err := (OCIReplication{Store: store, Source: objects, Destination: objects}).publish(ctx, repository.ReplicationPlan{SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatOCI}, []repository.ReplicationCheckpoint{{SourceObjectKey: sourceKey, ObjectKey: targetKey, Digest: digest, Size: int64(len(body)), State: "verified"}})
	if err == nil {
		t.Fatal("expected publication to reject removed source manifest")
	}
}

func digestOCIReplicationBody(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
