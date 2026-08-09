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

func TestPostgresScheduledTaskSchedulerSubmitsAuditCleanup(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	policy, err := store.GetAuditRetentionPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy.Enabled, policy.KeepDays = true, 30
	if _, err = store.ReplaceAuditRetentionPolicy(ctx, policy, policy.Version); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	task, err := store.CreateScheduledTask(ctx, repository.ScheduledTask{
		ID: uuid.NewString(), Name: "integration-scheduler-" + uuid.NewString(),
		Kind: repository.ScheduledTaskAuditRetention, IntervalSeconds: 900,
		Enabled: true, NextRunAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.DeleteScheduledTask(ctx, task.ID) }()

	if err = (ScheduledTaskScheduler{Store: store, Now: func() time.Time { return now }}).RunDue(ctx, 1); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListScheduledTaskRuns(ctx, task.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].State != repository.ScheduledTaskSubmitted {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	jobs, err := store.ListAuditCleanupJobs(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, job := range jobs {
		if job.ID == runs[0].TargetID && job.IdempotencyKey == "scheduled-task:"+task.ID+":"+runs[0].ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("audit cleanup job for run %#v not found in %#v", runs[0], jobs)
	}
}
