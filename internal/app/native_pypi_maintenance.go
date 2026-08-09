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

type NativePyPIMaintenance struct {
	Store interface {
		repository.NativePyPIStore
		repository.LifecycleJobStore
	}
	Objects        OCIObjectStore
	RecoveryWindow time.Duration
	Now            func() time.Time
	Metrics        repository.BackgroundOperationMetrics
}

type pypiReclaimPayload struct {
	Format    repository.Format `json:"format"`
	ObjectKey string            `json:"objectKey"`
}

func (m NativePyPIMaintenance) Collect(ctx context.Context) error {
	window := m.RecoveryWindow
	if window <= 0 {
		window = 24 * time.Hour
	}
	now := time.Now().UTC()
	if m.Now != nil {
		now = m.Now().UTC()
	}
	if err := m.EnqueueReclaimJobs(ctx, now.Add(-window), 200); err != nil {
		return err
	}
	return m.RunReclaimJobs(ctx, 200)
}

func (m NativePyPIMaintenance) EnqueueReclaimJobs(ctx context.Context, before time.Time, limit int) error {
	objects, err := m.Store.ListReclaimablePyPIObjects(ctx, before, limit)
	if err != nil {
		return err
	}
	for _, object := range objects {
		payload, _ := json.Marshal(pypiReclaimPayload{Format: repository.FormatPyPI, ObjectKey: object.ObjectKey})
		if _, _, err = m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: object.RepositoryID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "pypi:" + object.ObjectKey, Payload: payload}); err != nil && !errors.Is(err, repository.ErrIdempotencyConflict) {
			return err
		}
	}
	return nil
}

func (m NativePyPIMaintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatPyPI, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		m.begin()
		var payload pypiReclaimPayload
		if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != repository.FormatPyPI || payload.ObjectKey == "" {
			err = m.fail(ctx, job, "invalid PyPI reclaim payload")
			m.end("failed")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		release, lockErr := m.Store.LockPyPIObject(ctx, payload.ObjectKey)
		if lockErr != nil {
			err = m.fail(ctx, job, "PyPI object coordination failed")
			m.end("failed")
			if firstErr == nil {
				firstErr = err
			}
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

func (m NativePyPIMaintenance) runReclaimJob(ctx context.Context, job repository.LifecycleJob, payload pypiReclaimPayload) error {
	referenced, lookupErr := m.Store.PyPIObjectHasVisibleReference(ctx, payload.ObjectKey)
	if lookupErr != nil {
		err := m.fail(ctx, job, "PyPI object reference check failed")
		m.end("failed")
		return err
	}
	if referenced {
		err := m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
		if err != nil {
			m.end("failed")
			return err
		}
		m.end("completed")
		return nil
	}
	var err error
	if m.Objects == nil {
		err = m.fail(ctx, job, "PyPI object store is unavailable")
	} else if deleteErr := m.Objects.Delete(ctx, payload.ObjectKey); deleteErr != nil {
		err = m.fail(ctx, job, "delete PyPI object failed")
	} else if markErr := m.Store.MarkPyPIObjectCollected(ctx, payload.ObjectKey); markErr != nil && !errors.Is(markErr, repository.ErrNotFound) {
		err = m.fail(ctx, job, "mark PyPI object collected failed")
	} else {
		err = m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
	}
	if err != nil {
		m.end("failed")
		return err
	} else {
		m.end("completed")
		return nil
	}
}

func (m NativePyPIMaintenance) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message)
	return fmt.Errorf("%s", message)
}

func (m NativePyPIMaintenance) begin() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatPyPI, "started")
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatPyPI, 1)
	}
}

func (m NativePyPIMaintenance) end(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatPyPI, outcome)
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatPyPI, -1)
	}
}

func (m NativePyPIMaintenance) StartScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		window := m.RecoveryWindow
		if window <= 0 {
			window = 24 * time.Hour
		}
		_ = m.EnqueueReclaimJobs(ctx, time.Now().UTC().Add(-window), 200)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.EnqueueReclaimJobs(ctx, time.Now().UTC().Add(-window), 200)
			}
		}
	}()
}

func (m NativePyPIMaintenance) StartWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = m.RunReclaimJobs(ctx, 200)
		wake := notificationWake(ctx, m.Store, "artifact_gateway_lifecycle_jobs")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.RunReclaimJobs(ctx, 200)
			case <-wake:
				_ = m.RunReclaimJobs(ctx, 200)
			}
		}
	}()
}
