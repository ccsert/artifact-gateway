//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresReplicationPlanPersistsArtifactIdentity(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-id-source-" + uuid.NewString(), Format: FormatRaw})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-id-target-" + uuid.NewString(), Format: FormatRaw})
	if err != nil {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM hosted_repositories WHERE id=$1`, source.ID)
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM replication_plans WHERE source_repository_id=$1 OR target_repository_id=$2`, source.ID, target.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id IN ($1,$2)`, source.ID, target.ID)
		_ = store.Close()
	})

	digest := "sha256:" + strings.Repeat("a", 64)
	plan := ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID,
		Format: FormatRaw, Coordinate: "releases/widget.bin", Digest: digest, IdempotencyKey: "identity-" + uuid.NewString(),
	}
	checks := []ReplicationCheckpoint{{ObjectKey: "native/raw/widget", Digest: digest, Size: 12}}
	created, replayed, err := store.CreateReplicationPlan(ctx, plan, checks)
	if err != nil || replayed || created.Coordinate != plan.Coordinate || created.Digest != plan.Digest {
		t.Fatalf("created=%#v replayed=%t err=%v", created, replayed, err)
	}
	if replay, replayed, err := store.CreateReplicationPlan(ctx, plan, checks); err != nil || !replayed || replay.Coordinate != plan.Coordinate || replay.Digest != plan.Digest {
		t.Fatalf("replay=%#v replayed=%t err=%v", replay, replayed, err)
	}
	changed := plan
	changed.Coordinate = "releases/other.bin"
	if _, _, err := store.CreateReplicationPlan(ctx, changed, checks); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("coordinate replay err=%v", err)
	}
	changed = plan
	changed.Digest = "sha256:" + strings.Repeat("b", 64)
	if _, _, err := store.CreateReplicationPlan(ctx, changed, checks); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("digest replay err=%v", err)
	}

	claimed, err := store.ClaimReplicationPlansByFormat(ctx, FormatRaw, 1)
	if err != nil || len(claimed) != 1 || claimed[0].Coordinate != plan.Coordinate || claimed[0].Digest != plan.Digest {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	loaded, err := store.GetReplicationPlan(ctx, target.ID, plan.ID)
	if err != nil || loaded.Coordinate != plan.Coordinate || loaded.Digest != plan.Digest {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
}

func TestPostgresReplicationQuarantineParksWithoutConsumingAttemptAndReplayRequeues(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-park-source-" + uuid.NewString(), Format: FormatRaw})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-park-target-" + uuid.NewString(), Format: FormatRaw})
	if err != nil {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM hosted_repositories WHERE id=$1`, source.ID)
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM replication_plans WHERE source_repository_id=$1 OR target_repository_id=$2`, source.ID, target.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id IN ($1,$2)`, source.ID, target.ID)
		_ = store.Close()
	})

	digest := "sha256:" + strings.Repeat("d", 64)
	plan := ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID,
		Format: FormatRaw, Coordinate: "releases/quarantined.bin", Digest: digest,
		IdempotencyKey: "quarantine-park-" + uuid.NewString(), MaxAttempts: 2,
	}
	checkpoints := []ReplicationCheckpoint{{ObjectKey: "native/raw/quarantined", Digest: digest, Size: 12}}
	if _, replayed, err := store.CreateReplicationPlan(ctx, plan, checkpoints); err != nil || replayed {
		t.Fatalf("create replayed=%t err=%v", replayed, err)
	}
	claimed, err := store.ClaimReplicationPlansByFormat(ctx, FormatRaw, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != plan.ID || claimed[0].Attempts != 1 {
		t.Fatalf("initial claim=%#v err=%v", claimed, err)
	}
	if err = store.ParkReplicationPlanWithLease(ctx, plan.ID, ArtifactQuarantinedReason, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	parked, err := store.GetReplicationPlan(ctx, target.ID, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parked.State != "failed" || parked.LastError != ArtifactQuarantinedReason || parked.Attempts != 0 || !parked.NextAttemptAt.IsZero() || parked.LeaseToken != "" || !parked.LeaseExpiresAt.IsZero() {
		t.Fatalf("parked=%#v", parked)
	}
	if got, err := store.ClaimReplicationPlansByFormat(ctx, FormatRaw, 1); err != nil || len(got) != 0 {
		t.Fatalf("parked claim=%#v err=%v", got, err)
	}
	changedCheckpoints := []ReplicationCheckpoint{{ObjectKey: "native/raw/changed", Digest: digest, Size: 12}}
	if _, _, err = store.CreateReplicationPlan(ctx, plan, changedCheckpoints); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("non-PyPI parked replay with changed checkpoints err=%v", err)
	}

	type replayResult struct {
		plan     ReplicationPlan
		replayed bool
		err      error
	}
	results := make(chan replayResult, 2)
	for range 2 {
		go func() {
			requeued, replayed, replayErr := store.CreateReplicationPlan(ctx, plan, checkpoints)
			results <- replayResult{plan: requeued, replayed: replayed, err: replayErr}
		}()
	}
	for range 2 {
		result := <-results
		if result.err != nil || !result.replayed || result.plan.State != "pending" || result.plan.LastError != "" || result.plan.Attempts != 0 || result.plan.NextAttemptAt.IsZero() || !result.plan.StartedAt.IsZero() || !result.plan.CompletedAt.IsZero() {
			t.Fatalf("concurrent replay=%#v replayed=%t err=%v", result.plan, result.replayed, result.err)
		}
	}

	claimed, err = store.ClaimReplicationPlansByFormat(ctx, FormatRaw, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != plan.ID || claimed[0].Attempts != 1 {
		t.Fatalf("requeued claim=%#v err=%v", claimed, err)
	}
	if err = store.FailReplicationPlanWithLease(ctx, plan.ID, ArtifactQuarantinedReason, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	failed, err := store.GetReplicationPlan(ctx, target.ID, plan.ID)
	if err != nil || failed.State != "failed" || failed.LastError != ArtifactQuarantinedReason || failed.Attempts != 1 || failed.NextAttemptAt.IsZero() {
		t.Fatalf("ordinary failed=%#v err=%v", failed, err)
	}
	if replay, replayed, err := store.CreateReplicationPlan(ctx, plan, checkpoints); err != nil || !replayed || replay.State != "failed" || replay.NextAttemptAt.IsZero() {
		t.Fatalf("scheduled failed replay=%#v replayed=%t err=%v", replay, replayed, err)
	}
	claimed, err = store.ClaimReplicationPlansByFormat(ctx, FormatRaw, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != plan.ID || claimed[0].Attempts != 2 {
		t.Fatalf("ordinary retry claim=%#v err=%v", claimed, err)
	}
	if err = store.ParkReplicationPlanWithLease(ctx, plan.ID, "manual hold", claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	held, err := store.GetReplicationPlan(ctx, target.ID, plan.ID)
	if err != nil || held.State != "failed" || held.LastError != "manual hold" || held.Attempts != 1 || !held.NextAttemptAt.IsZero() {
		t.Fatalf("held=%#v err=%v", held, err)
	}
	if got, err := store.ClaimReplicationPlansByFormat(ctx, FormatRaw, 1); err != nil || len(got) != 0 {
		t.Fatalf("held claim=%#v err=%v", got, err)
	}
}

func TestPostgresParkedPyPIReplicationReplayRefreshesCheckpoints(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-refresh-source-" + uuid.NewString(), Format: FormatPyPI})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-refresh-target-" + uuid.NewString(), Format: FormatPyPI})
	if err != nil {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM hosted_repositories WHERE id=$1`, source.ID)
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM replication_plans WHERE source_repository_id=$1 OR target_repository_id=$2`, source.ID, target.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id IN ($1,$2)`, source.ID, target.ID)
		_ = store.Close()
	})

	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	plan := ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID,
		Format: FormatPyPI, Coordinate: "widget@1.0.0", Digest: digestA,
		IdempotencyKey: "pypi-checkpoint-refresh-" + uuid.NewString(), MaxAttempts: 2,
	}
	initial := []ReplicationCheckpoint{{
		SourceObjectKey: "native/pypi/sha256/a", ObjectKey: "replication/target/widget-a.whl", Digest: digestA, Size: 12,
	}}
	if _, replayed, createErr := store.CreateReplicationPlan(ctx, plan, initial); createErr != nil || replayed {
		t.Fatalf("create replayed=%t err=%v", replayed, createErr)
	}
	claimed, err := store.ClaimReplicationPlansByFormat(ctx, FormatPyPI, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != plan.ID {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}
	completedA := initial[0]
	completedA.PlanID = plan.ID
	completedA.ByteOffset = completedA.Size
	completedA.State = "verified"
	completedA.Attempts = 1
	completedA.VerifiedAt = time.Now().UTC()
	if err = store.UpdateReplicationCheckpointWithLease(ctx, completedA, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = store.ParkReplicationPlanWithLease(ctx, plan.ID, ArtifactQuarantinedReason, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}

	refreshed := []ReplicationCheckpoint{
		initial[0],
		{SourceObjectKey: "native/pypi/sha256/b", ObjectKey: "replication/target/widget-b.tar.gz", Digest: digestB, Size: 34},
	}
	replayRequest := plan
	replayRequest.ID = uuid.NewString()
	type refreshReplayResult struct {
		plan     ReplicationPlan
		replayed bool
		err      error
	}
	refreshResults := make(chan refreshReplayResult, 2)
	for range 2 {
		go func() {
			requeued, replayed, replayErr := store.CreateReplicationPlan(ctx, replayRequest, refreshed)
			refreshResults <- refreshReplayResult{plan: requeued, replayed: replayed, err: replayErr}
		}()
	}
	for range 2 {
		result := <-refreshResults
		if result.err != nil || !result.replayed || result.plan.ID != plan.ID || result.plan.State != "pending" {
			t.Fatalf("concurrent refresh=%#v replayed=%t err=%v", result.plan, result.replayed, result.err)
		}
	}
	checks, err := store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checks) != 2 {
		t.Fatalf("refreshed checkpoints=%#v err=%v", checks, err)
	}
	for index, checkpoint := range checks {
		if checkpoint.PlanID != plan.ID || checkpoint.State != "pending" || checkpoint.ByteOffset != 0 || checkpoint.Attempts != 0 || checkpoint.LastError != "" || !checkpoint.VerifiedAt.IsZero() || checkpoint.UpdatedAt.IsZero() {
			t.Fatalf("checkpoint[%d]=%#v", index, checkpoint)
		}
	}
	if checks[0].ObjectKey != refreshed[0].ObjectKey || checks[1].ObjectKey != refreshed[1].ObjectKey {
		t.Fatalf("refreshed checkpoints=%#v", checks)
	}

	claimed, err = store.ClaimReplicationPlansByFormat(ctx, FormatPyPI, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != plan.ID {
		t.Fatalf("refreshed claim=%#v err=%v", claimed, err)
	}
	if err = store.ParkReplicationPlanWithLease(ctx, plan.ID, ReplicationSnapshotChangedReason, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	digestC := "sha256:" + strings.Repeat("c", 64)
	refreshedAgain := append(append([]ReplicationCheckpoint(nil), refreshed...), ReplicationCheckpoint{
		SourceObjectKey: "native/pypi/sha256/c", ObjectKey: "replication/target/widget-c.whl", Digest: digestC, Size: 56,
	})
	replayRequest.ID = uuid.NewString()
	requeued, replayed, err := store.CreateReplicationPlan(ctx, replayRequest, refreshedAgain)
	if err != nil || !replayed || requeued.ID != plan.ID || requeued.State != "pending" {
		t.Fatalf("snapshot requeue=%#v replayed=%t err=%v", requeued, replayed, err)
	}
	checks, err = store.ListReplicationCheckpoints(ctx, plan.ID)
	if err != nil || len(checks) != 3 || checks[2].ObjectKey != refreshedAgain[2].ObjectKey {
		t.Fatalf("snapshot-refreshed checkpoints=%#v err=%v", checks, err)
	}
}

func TestPostgresReplicationPlanReadsLegacyEmptyIdentity(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-legacy-source-" + uuid.NewString(), Format: FormatRaw})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "replication-legacy-target-" + uuid.NewString(), Format: FormatRaw})
	if err != nil {
		_, _ = store.db.ExecContext(ctx, `DELETE FROM hosted_repositories WHERE id=$1`, source.ID)
		_ = store.Close()
		t.Fatal(err)
	}
	planID := uuid.NewString()
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM replication_plans WHERE id=$1`, planID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id IN ($1,$2)`, source.ID, target.ID)
		_ = store.Close()
	})

	if _, err = store.db.ExecContext(ctx, `INSERT INTO replication_plans (id,source_repository_id,target_repository_id,format,idempotency_key) VALUES ($1,$2,$3,$4,$5)`, planID, source.ID, target.ID, FormatRaw, "legacy-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetReplicationPlan(ctx, target.ID, planID)
	if err != nil || loaded.Coordinate != "" || loaded.Digest != "" {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}

	validDigest := "sha256:" + strings.Repeat("e", 64)
	for _, test := range []struct {
		name, coordinate, digest string
	}{
		{name: "coordinate only", coordinate: "releases/half.bin"},
		{name: "digest only", digest: validDigest},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, insertErr := store.db.ExecContext(ctx, `INSERT INTO replication_plans
				(id,source_repository_id,target_repository_id,format,coordinate,digest,idempotency_key)
				VALUES ($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), source.ID, target.ID, FormatRaw, test.coordinate, test.digest, "invalid-half-"+uuid.NewString())
			if insertErr == nil {
				t.Fatal("replication plan with half an artifact identity was accepted")
			}
		})
	}
}
