package repository

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryScheduledTasksClaimOnceAndAdvanceFromNow(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC)
	created, err := store.CreateScheduledTask(ctx, ScheduledTask{ID: "task", Name: "retention", Kind: ScheduledTaskAuditRetention, IntervalSeconds: 3600, Enabled: true, NextRunAt: now.Add(-24 * time.Hour)})
	if err != nil || created.Version != "1" {
		t.Fatalf("create task = %#v, %v", created, err)
	}
	claims, err := store.ClaimDueScheduledTasks(ctx, now, 10)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claims = %#v, %v", claims, err)
	}
	if got, want := claims[0].Task.NextRunAt, now.Add(time.Hour); !got.Equal(want) {
		t.Fatalf("next run = %v, want %v", got, want)
	}
	if second, claimErr := store.ClaimDueScheduledTasks(ctx, now, 10); claimErr != nil || len(second) != 0 {
		t.Fatalf("second claims = %#v, %v", second, claimErr)
	}
	run := claims[0].Run
	run.State, run.TargetKind, run.TargetID, run.CompletedAt = ScheduledTaskSubmitted, "audit-cleanup", "job-1", now
	if err = store.UpdateScheduledTaskRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetScheduledTask(ctx, created.ID)
	if err != nil || updated.LastRunState != ScheduledTaskSubmitted || updated.LastRunID != run.ID {
		t.Fatalf("updated task = %#v, %v", updated, err)
	}
}

func TestMemoryScheduledTasksUseOptimisticUpdatesAndManualHistory(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now().UTC()
	task, err := store.CreateScheduledTask(ctx, ScheduledTask{ID: "task", Name: "retention", Kind: ScheduledTaskRepositoryRetention, RepositoryID: "repo", IntervalSeconds: 900, Enabled: false, NextRunAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	task.Name = "renamed"
	updated, err := store.UpdateScheduledTask(ctx, task, task.Version)
	if err != nil || updated.Version != "2" {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	if _, err = store.UpdateScheduledTask(ctx, task, task.Version); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale update = %v", err)
	}
	run, err := store.CreateScheduledTaskRun(ctx, task.ID, "manual", now)
	if err != nil || run.Trigger != "manual" {
		t.Fatalf("manual run = %#v, %v", run, err)
	}
	runs, err := store.ListScheduledTaskRuns(ctx, task.ID, 10)
	if err != nil || len(runs) != 1 || runs[0].ID != run.ID {
		t.Fatalf("runs = %#v, %v", runs, err)
	}
	if err = store.DeleteScheduledTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetScheduledTask(ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted = %v", err)
	}
}
