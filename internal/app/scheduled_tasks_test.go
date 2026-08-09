package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestScheduledTaskSchedulerDispatchesDueAuditRetention(t *testing.T) {
	store := repository.NewMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	policy, err := store.GetAuditRetentionPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy.Enabled, policy.KeepDays = true, 30
	if _, err = store.ReplaceAuditRetentionPolicy(ctx, policy, policy.Version); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateScheduledTask(ctx, repository.ScheduledTask{ID: "task", Name: "audit cleanup", Kind: repository.ScheduledTaskAuditRetention, IntervalSeconds: 3600, Enabled: true, NextRunAt: now})
	if err != nil {
		t.Fatal(err)
	}
	clock := now
	scheduler := ScheduledTaskScheduler{Store: store, Now: func() time.Time { return clock }}
	if err = scheduler.RunDue(ctx, 10); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListScheduledTaskRuns(ctx, task.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].State != repository.ScheduledTaskSubmitted || runs[0].TargetKind != "audit-cleanup" || runs[0].TargetID == "" {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	jobs, err := store.ListAuditCleanupJobs(ctx, 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != runs[0].TargetID {
		t.Fatalf("jobs = %#v, %v", jobs, err)
	}
}

func TestScheduledTaskSchedulerRecordsDispatchFailureAndAllowsManualRetry(t *testing.T) {
	store := repository.NewMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	task, err := store.CreateScheduledTask(ctx, repository.ScheduledTask{ID: "task", Name: "audit cleanup", Kind: repository.ScheduledTaskAuditRetention, IntervalSeconds: 3600, Enabled: true, NextRunAt: now})
	if err != nil {
		t.Fatal(err)
	}
	clock := now
	scheduler := ScheduledTaskScheduler{Store: store, Now: func() time.Time { return clock }}
	if err = scheduler.RunDue(ctx, 10); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("run due error = %v", err)
	}
	runs, err := store.ListScheduledTaskRuns(ctx, task.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].State != repository.ScheduledTaskFailed || runs[0].LastError == "" {
		t.Fatalf("failed runs = %#v, %v", runs, err)
	}
	clock = clock.Add(time.Second)
	if _, err = scheduler.RunNow(ctx, task.ID); err == nil {
		t.Fatal("manual run succeeded with disabled policy")
	}
	runs, err = store.ListScheduledTaskRuns(ctx, task.ID, 10)
	if err != nil || len(runs) != 2 || runs[0].Trigger != "manual" {
		t.Fatalf("manual runs = %#v, %v", runs, err)
	}
}

func TestScheduledTaskSchedulerStartDispatchesImmediately(t *testing.T) {
	store := repository.NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	policy, err := store.GetAuditRetentionPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy.Enabled, policy.KeepDays = true, 30
	if _, err = store.ReplaceAuditRetentionPolicy(ctx, policy, policy.Version); err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateScheduledTask(ctx, repository.ScheduledTask{ID: "task", Name: "audit cleanup", Kind: repository.ScheduledTaskAuditRetention, IntervalSeconds: 3600, Enabled: true, NextRunAt: now})
	if err != nil {
		t.Fatal(err)
	}

	ScheduledTaskScheduler{Store: store, Now: func() time.Time { return now }}.Start(ctx, time.Hour)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		runs, listErr := store.ListScheduledTaskRuns(ctx, task.ID, 10)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(runs) == 1 {
			if runs[0].State != repository.ScheduledTaskSubmitted {
				t.Fatalf("run = %#v", runs[0])
			}
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("scheduler did not dispatch the due task on startup")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
