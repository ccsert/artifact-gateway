//go:build integration

package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresReplicationPlansPersistCheckpointsAndRetry(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	ctx := context.Background()
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-source-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "replication-target-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	plan := repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatRaw, IdempotencyKey: "replication-" + uuid.NewString()}
	checks := []repository.ReplicationCheckpoint{
		{ObjectKey: "native/raw/a", Digest: "sha256:" + strings.Repeat("a", 64), Size: 3},
		{ObjectKey: "native/raw/b", Digest: "sha256:" + strings.Repeat("b", 64), Size: 5},
	}
	created, replayed, err := store.CreateReplicationPlan(ctx, plan, checks)
	if err != nil || replayed || created.State != "pending" {
		t.Fatalf("created=%#v replayed=%t err=%v", created, replayed, err)
	}
	if replay, replayed, err := store.CreateReplicationPlan(ctx, plan, checks); err != nil || !replayed || replay.ID != plan.ID {
		t.Fatalf("replay=%#v replayed=%t err=%v", replay, replayed, err)
	}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []repository.ReplicationCheckpoint{{ObjectKey: "native/raw/a", Digest: "sha256:" + strings.Repeat("c", 64), Size: 3}}); !errors.Is(err, repository.ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay err=%v", err)
	}
	claimed, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != plan.ID || claimed[0].State != "running" {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = store.UpdateReplicationCheckpoint(ctx, repository.ReplicationCheckpoint{PlanID: plan.ID, ObjectKey: checks[0].ObjectKey, Digest: checks[0].Digest, Size: checks[0].Size, ByteOffset: checks[0].Size, State: "verified", Attempts: 1, VerifiedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err = store.FailReplicationPlan(ctx, plan.ID, "temporary object-store failure"); err != nil {
		t.Fatal(err)
	}
	retried, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(retried) != 1 || retried[0].ID != plan.ID || retried[0].LastError != "" {
		t.Fatalf("retried=%#v err=%v", retried, err)
	}
	persisted, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(persisted) != 2 || persisted[0].State != "verified" || persisted[0].ByteOffset != checks[0].Size || persisted[0].VerifiedAt.IsZero() {
		t.Fatalf("checkpoints=%#v err=%v", persisted, err)
	}
	if err = store.CompleteReplicationPlan(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
}
