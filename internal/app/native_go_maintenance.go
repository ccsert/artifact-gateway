package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// NativeGoMaintenance executes the durable recovery intents created before a
// Hosted Go publication writes a new content-addressed object. The publication
// object lock keeps the worker behind the publishing request: committed objects
// are retained, while objects without a database reference are reclaimed.
type NativeGoMaintenance struct {
	Store interface {
		repository.NativeGoStore
		repository.LifecycleJobStore
	}
	Objects OCIObjectStore
	Metrics repository.BackgroundOperationMetrics
}

type goReclaimPayload struct {
	Format    repository.Format `json:"format"`
	ObjectKey string            `json:"objectKey"`
}

func (m NativeGoMaintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatGo, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		m.begin()
		var payload goReclaimPayload
		if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != repository.FormatGo || payload.ObjectKey == "" {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "invalid Go reclaim payload")
			m.end("failed")
			continue
		}
		release, lockErr := m.Store.LockGoObject(ctx, payload.ObjectKey)
		if lockErr != nil {
			jobErr := m.fail(ctx, job, "Go object coordination failed")
			if firstErr == nil {
				firstErr = jobErr
			}
			m.end("failed")
			continue
		}
		err = m.runReclaimJob(ctx, job, payload)
		release()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m NativeGoMaintenance) runReclaimJob(ctx context.Context, job repository.LifecycleJob, payload goReclaimPayload) error {
	referenced, err := m.Store.GoModuleObjectHasReference(ctx, payload.ObjectKey)
	if err != nil {
		jobErr := m.fail(ctx, job, "Go object reference lookup failed")
		m.end("failed")
		return jobErr
	}
	if !referenced {
		if err = m.Objects.Delete(ctx, payload.ObjectKey); err != nil {
			jobErr := m.fail(ctx, job, fmt.Sprintf("delete Go publication object: %v", err))
			m.end("failed")
			return jobErr
		}
	}
	if err = m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
		m.end("failed")
		return err
	}
	m.end("completed")
	return nil
}

func (m NativeGoMaintenance) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message)
	return fmt.Errorf("%s", message)
}

func (m NativeGoMaintenance) begin() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatGo, "started")
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatGo, 1)
	}
}

func (m NativeGoMaintenance) end(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatGo, outcome)
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatGo, -1)
	}
}

func (m NativeGoMaintenance) StartWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = m.RunReclaimJobs(ctx, 100)
		wake := notificationWake(ctx, m.Store, "artifact_gateway_lifecycle_jobs")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.RunReclaimJobs(ctx, 100)
			case <-wake:
				_ = m.RunReclaimJobs(ctx, 100)
			}
		}
	}()
}
