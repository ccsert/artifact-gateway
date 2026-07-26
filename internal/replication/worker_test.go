package replication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type failingDestination struct {
	objectstore.Store
	failOnPut int
	puts      int
}

func (s *failingDestination) Put(ctx context.Context, key string, value []byte) error {
	s.puts++
	if s.failOnPut == s.puts {
		return errors.New("injected object-store failure")
	}
	return s.Store.Put(ctx, key, value)
}

func TestWorkerResumesFromCheckpointAndVerifiesSHA256(t *testing.T) {
	ctx := context.Background()
	source := objectstore.NewMemoryStore()
	destination := &failingDestination{Store: objectstore.NewMemoryStore()}
	body := []byte("checkpointed replication body")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := source.Put(ctx, "objects/widget", body); err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{ID: "plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatRaw, IdempotencyKey: "key"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: "objects/widget", Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	// The second chunk fails after the first offset has been durably saved.
	destination.failOnPut = 2
	worker := Worker{Store: store, Source: source, Destination: destination, ChunkBytes: 3}
	if err := worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checks) != 1 || checks[0].State != "failed" || checks[0].ByteOffset != 3 || checks[0].Attempts != 1 {
		t.Fatalf("failed checkpoint=%#v err=%v", checks, err)
	}
	if err = worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err = store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || checks[0].State != "verified" || checks[0].ByteOffset != int64(len(body)) || checks[0].VerifiedAt.IsZero() {
		t.Fatalf("verified checkpoint=%#v err=%v", checks, err)
	}
	got, err := destination.Get(ctx, "objects/widget")
	if err != nil || string(got) != string(body) {
		t.Fatalf("destination=%q err=%v", got, err)
	}
}

func TestWorkerRejectsInvalidDigest(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{ID: "invalid", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatRaw, IdempotencyKey: "invalid"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: "object", Digest: "sha256:" + strings.Repeat("x", 64), Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := (Worker{Store: store, Source: objectstore.NewMemoryStore(), Destination: objectstore.NewMemoryStore()}).Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || checks[0].State != "failed" || checks[0].Attempts != 1 {
		t.Fatalf("checks=%#v err=%v", checks, err)
	}
}
