package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRawReplicationPublishesVerifiedTargetAsset(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	body := []byte("replicated Raw content")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "native/raw/sha256/" + hex.EncodeToString(sum[:])
	if err := objects.PutVerifiedReader(ctx, key, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	asset := repository.RawAsset{RepositoryID: "source", Path: "releases/widget.txt", ObjectKey: key, Digest: digest, Size: int64(len(body)), ContentType: "text/plain"}
	if _, err := store.PutRawAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{ID: "plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatRaw, IdempotencyKey: "key"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: key, Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	if err := (RawReplication{Store: store, Source: objects, Destination: objects, ChunkBytes: 3}).RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	target, err := store.GetRawAsset(ctx, "target", asset.Path)
	if err != nil || target.Digest != digest || target.ObjectKey != key {
		t.Fatalf("target=%#v err=%v", target, err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || checks[0].State != "verified" {
		t.Fatalf("checks=%#v err=%v", checks, err)
	}
}
