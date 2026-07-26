package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

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

func TestReplicationStartsQueuedPlansImmediately(t *testing.T) {
	tests := []struct {
		name   string
		format repository.Format
		start  func(context.Context, *repository.MemoryStore, *MemoryOCIObjectStore)
	}{
		{name: "Raw", format: repository.FormatRaw, start: func(ctx context.Context, store *repository.MemoryStore, objects *MemoryOCIObjectStore) {
			RawReplication{Store: store, Source: objects, Destination: objects}.Start(ctx, time.Hour)
		}},
		{name: "Maven", format: repository.FormatMaven, start: func(ctx context.Context, store *repository.MemoryStore, objects *MemoryOCIObjectStore) {
			MavenReplication{Store: store, Source: objects, Destination: objects}.Start(ctx, time.Hour)
		}},
		{name: "Conan", format: repository.FormatConan, start: func(ctx context.Context, store *repository.MemoryStore, objects *MemoryOCIObjectStore) {
			ConanReplication{Store: store, Source: objects, Destination: objects}.Start(ctx, time.Hour)
		}},
		{name: "OCI", format: repository.FormatOCI, start: func(ctx context.Context, store *repository.MemoryStore, objects *MemoryOCIObjectStore) {
			OCIReplication{Store: store, Source: objects, Destination: objects}.Start(ctx, time.Hour)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := repository.NewMemoryStore()
			objects := NewMemoryOCIObjectStore()
			body := []byte("queued replication")
			sum := sha256.Sum256(body)
			digest := "sha256:" + hex.EncodeToString(sum[:])
			sourceKey := "source/" + test.name
			targetKey := "target/" + test.name
			if err := objects.PutVerifiedReader(ctx, sourceKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
				t.Fatal(err)
			}
			plan := repository.ReplicationPlan{ID: test.name, SourceRepositoryID: "source", TargetRepositoryID: "target", Format: test.format, IdempotencyKey: test.name}
			if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{SourceObjectKey: sourceKey, ObjectKey: targetKey, Digest: digest, Size: int64(len(body))}}); err != nil {
				t.Fatal(err)
			}

			test.start(ctx, store, objects)
			deadline := time.After(time.Second)
			for {
				checkpoints, err := store.ListReplicationCheckpoints(ctx, plan.ID)
				if err != nil {
					t.Fatal(err)
				}
				if len(checkpoints) == 1 && checkpoints[0].State == "verified" {
					return
				}
				select {
				case <-deadline:
					t.Fatalf("queued plan was not processed before the ticker: %#v", checkpoints)
				case <-time.After(time.Millisecond):
				}
			}
		})
	}
}
