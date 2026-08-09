package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type NativeNPMMaintenance struct {
	Store interface {
		repository.NativeNPMStore
		repository.LifecycleJobStore
	}
	Objects OCIObjectStore
	Now     func() time.Time
	Metrics repository.BackgroundOperationMetrics
}

type npmReclaimPayload struct {
	Format    repository.Format `json:"format"`
	ObjectKey string            `json:"objectKey"`
}

func (m NativeNPMMaintenance) Collect(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	if err := m.EnqueueReclaimJobs(ctx, now().UTC().Add(-24*time.Hour), 100); err != nil {
		return err
	}
	return m.RunReclaimJobs(ctx, 100)
}

func (m NativeNPMMaintenance) EnqueueReclaimJobs(ctx context.Context, before time.Time, limit int) error {
	objects, err := m.Store.ListReclaimableNPMObjects(ctx, before, limit)
	if err != nil {
		return err
	}
	for _, object := range objects {
		payload, _ := json.Marshal(npmReclaimPayload{Format: repository.FormatNPM, ObjectKey: object.ObjectKey})
		if _, _, err = m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: object.RepositoryID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "npm-object:" + object.ObjectKey, Payload: payload}); err != nil {
			return err
		}
	}
	return nil
}

func (m NativeNPMMaintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatNPM, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		m.begin()
		var payload npmReclaimPayload
		if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != repository.FormatNPM || payload.ObjectKey == "" {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "invalid npm reclaim payload")
			m.end("failed")
			continue
		}
		release, lockErr := m.Store.LockNPMObject(ctx, payload.ObjectKey)
		if lockErr != nil {
			firstErr = m.fail(ctx, job, "npm object coordination failed")
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

func (m NativeNPMMaintenance) runReclaimJob(ctx context.Context, job repository.LifecycleJob, payload npmReclaimPayload) error {
	referenced, lookupErr := m.Store.NPMObjectHasVisibleReference(ctx, payload.ObjectKey)
	if lookupErr != nil {
		err := m.fail(ctx, job, "npm object reference lookup failed")
		m.end("failed")
		return err
	}
	if referenced {
		if err := m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
			m.end("failed")
			return err
		}
		m.end("completed")
		return nil
	}
	if err := m.Objects.Delete(ctx, payload.ObjectKey); err != nil {
		jobErr := m.fail(ctx, job, fmt.Sprintf("delete npm object: %v", err))
		m.end("failed")
		return jobErr
	}
	if err := m.Store.MarkNPMObjectCollected(ctx, payload.ObjectKey); err != nil && !errors.Is(err, repository.ErrNotFound) {
		jobErr := m.fail(ctx, job, "mark npm object collected failed")
		m.end("failed")
		return jobErr
	}
	if err := m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
		m.end("failed")
		return err
	}
	m.end("completed")
	return nil
}

func (m NativeNPMMaintenance) begin() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatNPM, "started")
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatNPM, 1)
	}
}

func (m NativeNPMMaintenance) end(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatNPM, outcome)
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatNPM, -1)
	}
}

func (m NativeNPMMaintenance) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message)
	return fmt.Errorf("%s", message)
}

func (m NativeNPMMaintenance) StartScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now
				if m.Now != nil {
					now = m.Now
				}
				_ = m.EnqueueReclaimJobs(ctx, now().UTC().Add(-24*time.Hour), 100)
			}
		}
	}()
}

func (m NativeNPMMaintenance) StartWorker(ctx context.Context, interval time.Duration) {
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
