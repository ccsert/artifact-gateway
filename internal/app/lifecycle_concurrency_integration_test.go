//go:build integration

package app

import (
	"context"
	"os"
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
