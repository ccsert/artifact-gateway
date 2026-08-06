//go:build integration

package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresLifecycleClaimLimitsOneJobPerRepository(t *testing.T) {
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
	create := func(name string) repository.HostedRepository {
		repo, createErr := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: name + "-" + uuid.NewString(), Format: repository.FormatRaw})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return repo
	}
	first, second := create("lifecycle-first"), create("lifecycle-second")
	for _, job := range []repository.LifecycleJob{
		{ID: uuid.NewString(), RepositoryID: first.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "first-reclaim", Payload: []byte(`{"format":"raw"}`)},
		{ID: uuid.NewString(), RepositoryID: first.ID, Kind: repository.LifecycleJobPromotion, IdempotencyKey: "first-promotion", Payload: []byte(`{"format":"raw"}`)},
		{ID: uuid.NewString(), RepositoryID: second.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "second-reclaim", Payload: []byte(`{"format":"raw"}`)},
	} {
		if _, _, err = store.EnqueueLifecycleJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := store.ClaimLifecycleJobs(ctx, 10)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	count := map[string]int{}
	for _, job := range claimed {
		count[job.RepositoryID]++
	}
	if count[first.ID] != 1 || count[second.ID] != 1 {
		t.Fatalf("claims per repository=%v", count)
	}
}

func TestPostgresLifecycleRetriesAndRecoversExpiredLeases(t *testing.T) {
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
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "lifecycle-retry-" + uuid.NewString(), Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	jobID := uuid.NewString()
	created, _, err := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: jobID, RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: jobID, Payload: []byte(`{"format":"raw"}`), MaxAttempts: 2})
	if err != nil || created.MaxAttempts != 2 {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	claimed, err := store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 1 {
		t.Fatalf("first claim=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, jobID, claimed[0].LeaseToken, "temporary outage"); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetLifecycleJob(ctx, repo.ID, jobID)
	if err != nil || job.State != repository.LifecycleJobRetrying || !job.NextAttemptAt.After(time.Now().UTC()) {
		t.Fatalf("retrying=%#v err=%v", job, err)
	}
	if _, err = store.RunLifecycleJobNow(ctx, repo.ID, jobID); err != nil {
		t.Fatal(err)
	}
	claimed, err = store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 2 {
		t.Fatalf("second claim=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, jobID, claimed[0].LeaseToken, "permanent outage"); err != nil {
		t.Fatal(err)
	}
	job, err = store.GetLifecycleJob(ctx, repo.ID, jobID)
	if err != nil || job.State != repository.LifecycleJobFailed || job.CompletedAt.IsZero() {
		t.Fatalf("failed=%#v err=%v", job, err)
	}
	job, err = store.RetryLifecycleJob(ctx, repo.ID, jobID)
	if err != nil || job.State != repository.LifecycleJobPending || job.Attempts != 0 {
		t.Fatalf("retried=%#v err=%v", job, err)
	}
}

func TestPostgresBackgroundOperationQueueStatsIncludeLifecycleAndReplication(t *testing.T) {
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
	count := func(stats []repository.BackgroundOperationQueueStat, kind repository.BackgroundOperationKind, format repository.Format, state repository.LifecycleJobState) int64 {
		for _, stat := range stats {
			if stat.Kind == kind && stat.Format == format && stat.State == state {
				return stat.Count
			}
		}
		return 0
	}
	before, err := store.BackgroundOperationQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "queue-source-" + uuid.NewString(), Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "queue-target-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleJob, _, err := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: source.ID, Kind: repository.LifecycleJobPromotion, IdempotencyKey: uuid.NewString(), Payload: []byte(`{"format":"conan"}`)})
	if err != nil {
		t.Fatal(err)
	}
	replicationPlan, _, err := store.CreateReplicationPlan(ctx, repository.ReplicationPlan{ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatMaven, IdempotencyKey: uuid.NewString()}, []repository.ReplicationCheckpoint{{ObjectKey: "widget", Digest: "sha256:" + strings.Repeat("a", 64), Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.BackgroundOperationQueueStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := count(after, repository.BackgroundOperationPromotion, repository.FormatConan, repository.LifecycleJobPending), count(before, repository.BackgroundOperationPromotion, repository.FormatConan, repository.LifecycleJobPending)+1; got != want {
		t.Fatalf("promotion pending count=%d want=%d stats=%#v", got, want, after)
	}
	if got, want := count(after, repository.BackgroundOperationReplication, repository.FormatMaven, repository.LifecycleJobPending), count(before, repository.BackgroundOperationReplication, repository.FormatMaven, repository.LifecycleJobPending)+1; got != want {
		t.Fatalf("replication pending count=%d want=%d stats=%#v", got, want, after)
	}
	if _, err := store.CancelLifecycleJob(ctx, source.ID, lifecycleJob.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CancelReplicationPlan(ctx, source.ID, replicationPlan.ID); err != nil {
		t.Fatal(err)
	}
}
