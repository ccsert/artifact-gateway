package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type NativeConanMaintenance struct {
	Store interface {
		repository.NativeConanStore
		repository.LifecycleJobStore
	}
	Objects OCIObjectStore
	Now     func() time.Time
	Metrics repository.BackgroundOperationMetrics
}
type conanReclaimPayload struct {
	Format    repository.Format `json:"format"`
	ObjectKey string            `json:"objectKey"`
}

func (m NativeConanMaintenance) Collect(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	if err := m.EnqueueReclaimJobs(ctx, now().UTC().Add(-24*time.Hour), 100); err != nil {
		return err
	}
	return m.RunReclaimJobs(ctx, 100)
}
func (m NativeConanMaintenance) EnqueueReclaimJobs(ctx context.Context, before time.Time, limit int) error {
	objects, err := m.Store.ListReclaimableConanObjects(ctx, before, limit)
	if err != nil {
		return err
	}
	for _, object := range objects {
		payload, _ := json.Marshal(conanReclaimPayload{Format: repository.FormatConan, ObjectKey: object.ObjectKey})
		if _, _, err = m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: object.RepositoryID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "conan-object:" + object.ObjectKey, Payload: payload}); err != nil {
			return err
		}
	}
	return nil
}
func (m NativeConanMaintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatConan, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		m.begin()
		var payload conanReclaimPayload
		if json.Unmarshal(job.Payload, &payload) != nil || payload.ObjectKey == "" {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "invalid Conan reclaim payload")
			m.end("failed")
			continue
		}
		referenced, err := m.Store.ConanObjectHasVisibleReference(ctx, payload.ObjectKey)
		if err != nil {
			m.end("failed")
			if jobErr := m.fail(ctx, job, "Conan object reference lookup failed"); firstErr == nil {
				firstErr = jobErr
			}
			continue
		}
		if referenced {
			if err = m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
				m.end("failed")
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			m.end("completed")
			continue
		}
		if err = m.Objects.Delete(ctx, payload.ObjectKey); err != nil {
			m.end("failed")
			if jobErr := m.fail(ctx, job, fmt.Sprintf("delete Conan object: %v", err)); firstErr == nil {
				firstErr = jobErr
			}
			continue
		}
		if err = m.Store.MarkConanObjectCollected(ctx, payload.ObjectKey); err != nil && err != repository.ErrNotFound {
			m.end("failed")
			if jobErr := m.fail(ctx, job, "mark Conan object collected failed"); firstErr == nil {
				firstErr = jobErr
			}
			continue
		}
		if err = m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
			m.end("failed")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		m.end("completed")
	}
	return firstErr
}

func (m NativeConanMaintenance) begin() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatConan, "started")
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatConan, 1)
	}
}

func (m NativeConanMaintenance) end(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatConan, outcome)
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatConan, -1)
	}
}
func (m NativeConanMaintenance) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message)
	return fmt.Errorf("%s", message)
}
func (m NativeConanMaintenance) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.Collect(ctx)
			}
		}
	}()
}
