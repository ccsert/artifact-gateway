package repository

import (
	"context"
	"strings"
	"testing"
)

func TestMemoryBackgroundOperationQueueStatsReportPendingPromotion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	created, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{
		ID:             "promotion",
		RepositoryID:   "target",
		Kind:           LifecycleJobPromotion,
		IdempotencyKey: "promote-widget",
		Payload:        []byte(`{"format":"raw"}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	stats, err := store.BackgroundOperationQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Kind != "promotion" || stats[0].Format != FormatRaw || stats[0].State != LifecycleJobPending || stats[0].Count != 1 || !stats[0].OldestCreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("stats=%#v created=%#v", stats, created)
	}
}

func TestMemoryBackgroundOperationQueueStatsReportPendingReplication(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	digest := "sha256:" + strings.Repeat("a", 64)
	created, _, err := store.CreateReplicationPlan(ctx, ReplicationPlan{
		ID:                 "replication",
		SourceRepositoryID: "source",
		TargetRepositoryID: "target",
		Format:             FormatMaven,
		Coordinate:         "org.example:widget:1.0.0",
		Digest:             digest,
		IdempotencyKey:     "replicate-widget",
	}, []ReplicationCheckpoint{{ObjectKey: "widget", Digest: digest, Size: 1}})
	if err != nil {
		t.Fatal(err)
	}

	stats, err := store.BackgroundOperationQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Kind != BackgroundOperationReplication || stats[0].Format != FormatMaven || stats[0].State != LifecycleJobPending || stats[0].Count != 1 || !stats[0].OldestCreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("stats=%#v created=%#v", stats, created)
	}
}

func TestMemoryBackgroundOperationQueueStatsNormalizeRetryableReplicationFailure(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	digest := "sha256:" + strings.Repeat("b", 64)
	plan := ReplicationPlan{ID: "retrying", SourceRepositoryID: "source", TargetRepositoryID: "target", Format: FormatOCI, Coordinate: "library/widget", Digest: digest, IdempotencyKey: "retrying", MaxAttempts: 2}
	if _, _, err := store.CreateReplicationPlan(ctx, plan, []ReplicationCheckpoint{{ObjectKey: "widget", Digest: digest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimReplicationPlans(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = store.FailReplicationPlanWithLease(ctx, plan.ID, "temporary failure", claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}

	stats, err := store.BackgroundOperationQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].Kind != BackgroundOperationReplication || stats[0].Format != FormatOCI || stats[0].State != LifecycleJobRetrying || stats[0].Count != 1 {
		t.Fatalf("stats=%#v", stats)
	}
}
