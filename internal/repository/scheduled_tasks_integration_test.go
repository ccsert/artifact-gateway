//go:build integration

package repository

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresScheduledTaskClaimIsSingleConsumer(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	first, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	task, err := first.CreateScheduledTask(ctx, ScheduledTask{ID: uuid.NewString(), Name: "integration-schedule-" + uuid.NewString(), Kind: ScheduledTaskAuditRetention, IntervalSeconds: 900, Enabled: true, NextRunAt: now.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	defer first.DeleteScheduledTask(ctx, task.ID)

	type result struct {
		claims []ScheduledTaskClaim
		err    error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	claim := func(store *PostgresStore) {
		ready.Done()
		ready.Wait()
		claims, claimErr := store.ClaimDueScheduledTasks(ctx, now, 1)
		results <- result{claims: claims, err: claimErr}
	}
	go claim(first)
	go claim(second)
	claimed := make([]ScheduledTaskClaim, 0, 1)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		claimed = append(claimed, result.claims...)
	}
	if len(claimed) != 1 || claimed[0].Task.ID != task.ID || claimed[0].Run.Trigger != "scheduled" {
		t.Fatalf("claims = %#v", claimed)
	}
	if got, want := claimed[0].Task.NextRunAt, now.Add(15*time.Minute); !got.Equal(want) {
		t.Fatalf("next run = %v, want %v", got, want)
	}
	if claimed[0].Run.State != ScheduledTaskFailed || claimed[0].Run.LastError != "dispatch interrupted before submission" {
		t.Fatalf("claim placeholder = %#v", claimed[0].Run)
	}
	remaining, err := first.ClaimDueScheduledTasks(ctx, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("interrupted claim was reissued before next interval: %#v", remaining)
	}
	run := claimed[0].Run
	run.State, run.TargetKind, run.TargetID, run.CompletedAt = ScheduledTaskSubmitted, "audit-cleanup", uuid.NewString(), now
	if err = first.UpdateScheduledTaskRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	runs, err := first.ListScheduledTaskRuns(ctx, task.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].State != ScheduledTaskSubmitted || runs[0].TargetID != run.TargetID {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	updated, err := first.GetScheduledTask(ctx, task.ID)
	if err != nil || updated.LastRunState != ScheduledTaskSubmitted || updated.LastRunID != run.ID {
		t.Fatalf("updated = %#v, %v", updated, err)
	}
}
