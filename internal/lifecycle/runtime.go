package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const jobNotificationChannel = "artifact_gateway_lifecycle_jobs"

// Store is the narrow persistence seam required to execute lifecycle jobs.
type Store interface {
	ClaimLifecycleJobsByKindAndFormat(context.Context, repository.LifecycleJobKind, repository.Format, int) ([]repository.LifecycleJob, error)
	UpdateLifecycleJobProgress(context.Context, string, string, int, int, string) error
	RenewLifecycleJobLease(context.Context, string, string) error
	CompleteLifecycleJob(context.Context, string, string) error
	FailLifecycleJob(context.Context, string, string, string) error
}

// JobHandler performs the domain-specific work for one claimed job. The
// runtime records the terminal state after the handler returns.
type JobHandler func(context.Context, repository.LifecycleJob) error

// Runtime executes one lifecycle job kind across a configured set of formats.
// Name is used in operator-facing errors; Operation is the metrics label.
type Runtime struct {
	Store                Store
	Kind                 repository.LifecycleJobKind
	Formats              []repository.Format
	Name                 string
	Operation            string
	Metrics              repository.BackgroundOperationMetrics
	LeaseRefreshInterval time.Duration
	LeaseProgressMessage string
}

// RunJobs claims and executes at most limit jobs while preserving capacity
// across formats. A claim or execution failure does not starve later formats;
// the first observed error is returned after all available work is attempted.
func (r Runtime) RunJobs(ctx context.Context, limit int, handler JobHandler) error {
	if r.Store == nil || handler == nil {
		return errors.New("lifecycle job runtime is not configured")
	}
	if limit <= 0 {
		limit = 100
	}
	var firstErr error
	remaining := limit
	for _, format := range r.Formats {
		if remaining == 0 {
			break
		}
		jobs, err := r.Store.ClaimLifecycleJobsByKindAndFormat(ctx, r.Kind, format, remaining)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		remaining -= len(jobs)
		for _, job := range jobs {
			if err = r.runJob(ctx, format, job, handler); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (r Runtime) runJob(ctx context.Context, format repository.Format, job repository.LifecycleJob, handler JobHandler) error {
	if r.Metrics != nil {
		r.Metrics.RecordBackgroundOperation(r.Operation, format, "started")
		r.Metrics.AddBackgroundOperationInFlight(r.Operation, format, 1)
		defer r.Metrics.AddBackgroundOperationInFlight(r.Operation, format, -1)
	}
	jobCtx, cancelJob := context.WithCancel(ctx)
	if r.LeaseProgressMessage != "" {
		if err := r.Store.UpdateLifecycleJobProgress(jobCtx, job.ID, job.LeaseToken, job.ProgressCurrent, job.ProgressTotal, r.LeaseProgressMessage); err != nil {
			cancelJob()
			return r.fail(ctx, format, job, fmt.Errorf("%s lease renewal failed: %v", r.Name, err))
		}
	}
	stopHeartbeat := r.startLeaseHeartbeat(jobCtx, cancelJob, job)
	if err := handler(jobCtx, job); err != nil {
		if leaseErr := stopHeartbeat(); leaseErr != nil {
			err = fmt.Errorf("%s lease renewal failed: %v", r.Name, leaseErr)
		}
		return r.fail(ctx, format, job, err)
	}
	if leaseErr := stopHeartbeat(); leaseErr != nil {
		return r.fail(ctx, format, job, fmt.Errorf("%s lease renewal failed: %v", r.Name, leaseErr))
	}
	if err := r.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
		return err
	}
	if r.Metrics != nil {
		r.Metrics.RecordBackgroundOperation(r.Operation, format, "completed")
	}
	return nil
}

func (r Runtime) fail(ctx context.Context, format repository.Format, job repository.LifecycleJob, cause error) error {
	_ = r.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, cause.Error())
	if r.Metrics != nil {
		r.Metrics.RecordBackgroundOperation(r.Operation, format, "failed")
	}
	return cause
}

func (r Runtime) startLeaseHeartbeat(ctx context.Context, cancelJob context.CancelFunc, job repository.LifecycleJob) func() error {
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	result := make(chan error, 1)
	interval := r.LeaseRefreshInterval
	if interval <= 0 {
		interval = 3 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				result <- nil
				return
			case <-ticker.C:
				err := r.Store.RenewLifecycleJobLease(heartbeatCtx, job.ID, job.LeaseToken)
				if err == nil {
					continue
				}
				if heartbeatCtx.Err() != nil {
					result <- nil
					return
				}
				cancelJob()
				result <- err
				return
			}
		}
	}()
	var once sync.Once
	var stopErr error
	return func() error {
		once.Do(func() {
			cancelHeartbeat()
			cancelJob()
			stopErr = <-result
		})
		return stopErr
	}
}

// Start runs an immediate batch, then polls and reacts to PostgreSQL lifecycle
// notifications when the store exposes that adapter capability.
func (r Runtime) Start(ctx context.Context, interval time.Duration, limit int, handler JobHandler) {
	if interval <= 0 || r.Store == nil || handler == nil {
		return
	}
	go func() {
		wake := notificationWake(ctx, r.Store)
		_ = r.RunJobs(ctx, limit, handler)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.RunJobs(ctx, limit, handler)
			case _, ok := <-wake:
				if !ok {
					wake = nil
					continue
				}
				_ = r.RunJobs(ctx, limit, handler)
			}
		}
	}()
}

type notificationSource interface {
	Listen(context.Context, string) <-chan struct{}
}

func notificationWake(ctx context.Context, store Store) <-chan struct{} {
	if source, ok := store.(notificationSource); ok {
		return source.Listen(ctx, jobNotificationChannel)
	}
	return nil
}
