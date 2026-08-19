package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type scheduledTaskExecutionStore interface {
	repository.ScheduledTaskStore
	repository.HostedRepositoryStore
	repository.RepositoryRetentionPolicyStore
	repository.LifecycleJobStore
	repository.NativeMavenStore
	repository.NativeOCIStore
	repository.NativeRawStore
	repository.NativeConanStore
	repository.NativeNPMStore
	repository.NativePyPIStore
	repository.NativeGoStore
	repository.AuditRetentionStore
}

// ScheduledTaskScheduler dispatches operator-defined schedules into the
// existing durable job queues. It does not execute maintenance inline.
type ScheduledTaskScheduler struct {
	Store scheduledTaskExecutionStore
	Now   func() time.Time
}

func (s ScheduledTaskScheduler) RunDue(ctx context.Context, limit int) error {
	claims, err := s.Store.ClaimDueScheduledTasks(ctx, s.now(), limit)
	if err != nil {
		return err
	}
	var joined error
	for _, claim := range claims {
		if _, err = s.dispatch(ctx, claim.Task, claim.Run); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func (s ScheduledTaskScheduler) RunNow(ctx context.Context, taskID string) (repository.ScheduledTaskRun, error) {
	task, err := s.Store.GetScheduledTask(ctx, taskID)
	if err != nil {
		return repository.ScheduledTaskRun{}, err
	}
	run, err := s.Store.CreateScheduledTaskRun(ctx, taskID, "manual", s.now())
	if err != nil {
		return repository.ScheduledTaskRun{}, err
	}
	return s.dispatch(ctx, task, run)
}

func (s ScheduledTaskScheduler) dispatch(ctx context.Context, task repository.ScheduledTask, run repository.ScheduledTaskRun) (repository.ScheduledTaskRun, error) {
	key := "scheduled-task:" + task.ID + ":" + run.ID
	var targetKind, targetID string
	var dispatchErr error
	switch task.Kind {
	case repository.ScheduledTaskRepositoryRetention:
		var policy repository.RepositoryRetentionPolicy
		policy, dispatchErr = s.Store.GetRepositoryRetentionPolicy(ctx, task.RepositoryID)
		if dispatchErr == nil && !policy.Enabled {
			dispatchErr = errors.New("repository retention policy is disabled")
		}
		if dispatchErr == nil {
			var job repository.LifecycleJob
			job, _, dispatchErr = (NativeRepositoryRetention{Store: s.Store, Now: s.Now}).EnqueueRepository(ctx, task.RepositoryID, key)
			targetKind, targetID = "lifecycle", job.ID
		}
	case repository.ScheduledTaskAuditRetention:
		var policy repository.AuditRetentionPolicy
		policy, dispatchErr = s.Store.GetAuditRetentionPolicy(ctx)
		if dispatchErr == nil && !policy.Enabled {
			dispatchErr = errors.New("audit retention policy is disabled")
		}
		if dispatchErr == nil {
			var job repository.AuditCleanupJob
			job, _, dispatchErr = (AuditRetentionWorker{Store: s.Store}).Enqueue(ctx, key, 1000)
			targetKind, targetID = "audit-cleanup", job.ID
		}
	default:
		dispatchErr = fmt.Errorf("unsupported scheduled task kind %q", task.Kind)
	}
	run.CompletedAt = s.now()
	if dispatchErr != nil {
		run.State = repository.ScheduledTaskFailed
		run.LastError = truncateScheduledTaskError(dispatchErr.Error())
		if updateErr := s.Store.UpdateScheduledTaskRun(ctx, run); updateErr != nil {
			return run, errors.Join(dispatchErr, updateErr)
		}
		return run, dispatchErr
	}
	run.State, run.TargetKind, run.TargetID, run.LastError = repository.ScheduledTaskSubmitted, targetKind, targetID, ""
	if err := s.Store.UpdateScheduledTaskRun(ctx, run); err != nil {
		return run, err
	}
	return run, nil
}

func (s ScheduledTaskScheduler) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = s.RunDue(ctx, 100)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.RunDue(ctx, 100)
			}
		}
	}()
}

func (s ScheduledTaskScheduler) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func truncateScheduledTaskError(message string) string {
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}
