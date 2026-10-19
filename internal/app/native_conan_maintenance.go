package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type NativeConanMaintenance struct {
	Store interface {
		repository.NativeConanStore
		repository.LifecycleJobStore
	}
	Collections repository.ConanObjectCollectionStore
	Objects     ObjectStore
	Now         func() time.Time
	Metrics     repository.BackgroundOperationMetrics
}
type conanReclaimPayload struct {
	Format    repository.Format `json:"format"`
	ObjectKey string            `json:"objectKey"`
}

func (m NativeConanMaintenance) Collect(ctx context.Context) error {
	if nilDependency(m.Collections) || nilDependency(m.Objects) {
		return repository.ErrDisabled
	}
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
		if err = m.enqueueReclaimJob(ctx, object, payload); err != nil {
			return err
		}
	}
	return nil
}

func (m NativeConanMaintenance) enqueueReclaimJob(ctx context.Context, object repository.ConanObjectIntent, payload []byte) error {
	key := "conan-object:" + object.ObjectKey
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		job, replayed, err := m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: object.RepositoryID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: key, Payload: payload})
		if err != nil {
			return err
		}
		if !replayed || job.State != repository.LifecycleJobCompleted {
			return nil
		}
		// A completed attempt may have skipped deletion because a reference
		// won. A later tombstone needs another attempt. Deterministic successors
		// let concurrent schedulers converge without changing job retry policy.
		key = "conan-object:" + object.ObjectKey + ":after:" + job.ID
	}
}
func (m NativeConanMaintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	if nilDependency(m.Collections) || nilDependency(m.Objects) {
		return repository.ErrDisabled
	}
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
		collect, err := m.Collections.BeginConanObjectCollection(ctx, payload.ObjectKey)
		if errors.Is(err, repository.ErrNotFound) {
			collect, err = false, nil
		}
		if err != nil {
			m.end("failed")
			if jobErr := m.fail(ctx, job, "claim Conan object collection failed"); firstErr == nil {
				firstErr = jobErr
			}
			continue
		}
		if !collect {
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
		if err = m.Collections.CompleteConanObjectCollection(ctx, payload.ObjectKey); err != nil && !errors.Is(err, repository.ErrNotFound) {
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
	m.StartScheduler(ctx, interval)
	m.StartWorker(ctx, interval)
}

// StartScheduler discovers reclaimable Conan objects and records durable jobs.
func (m NativeConanMaintenance) StartScheduler(ctx context.Context, interval time.Duration) {
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

// StartWorker only claims and executes Conan reclaim jobs.
func (m NativeConanMaintenance) StartWorker(ctx context.Context, interval time.Duration) {
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
