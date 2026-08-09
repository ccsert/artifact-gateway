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
	if err := source.Put(ctx, "objects/source-widget", body); err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{ID: "plan", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: repository.FormatRaw, IdempotencyKey: "key"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{SourceObjectKey: "objects/source-widget", ObjectKey: "objects/widget", Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	locked, released := make([]string, 0), make([]string, 0)
	lockObject := func(_ context.Context, key string) (func(), error) {
		locked = append(locked, key)
		return func() { released = append(released, key) }, nil
	}
	// The second chunk fails after the first offset has been durably saved.
	destination.failOnPut = 2
	worker := Worker{Store: store, Source: source, Destination: destination, ChunkBytes: 3, LockObject: lockObject}
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
	if len(locked) != 4 || len(released) != 4 || locked[0] != "objects/source-widget" || locked[1] != "objects/widget" {
		t.Fatalf("object coordination locked=%v released=%v", locked, released)
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

func TestWorkerFailsPlanWhenObjectCoordinationFails(t *testing.T) {
	ctx := context.Background()
	source := objectstore.NewMemoryStore()
	body := []byte("replication")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if err := source.Put(ctx, "source", body); err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	plan := repository.ReplicationPlan{ID: "lock-failure", SourceRepositoryID: "source-repo", TargetRepositoryID: "target-repo", Format: repository.FormatNPM, IdempotencyKey: "lock-failure"}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{SourceObjectKey: "source", ObjectKey: "target", Digest: digest, Size: int64(len(body))}}); err != nil {
		t.Fatal(err)
	}
	worker := Worker{
		Store: store, Source: source, Destination: objectstore.NewMemoryStore(), Format: repository.FormatNPM,
		LockObject: func(context.Context, string) (func(), error) {
			return nil, errors.New("coordination unavailable")
		},
	}
	if err := worker.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	failed, err := store.GetReplicationPlan(ctx, plan.TargetRepositoryID, plan.ID)
	if err != nil || failed.State != "failed" || failed.LastError != "replication object coordination failed" {
		t.Fatalf("failed plan=%#v err=%v", failed, err)
	}
	if _, err = worker.Destination.Get(ctx, "target"); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("destination changed before lock: %v", err)
	}
}
