package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// NativeCargoMaintenance completes recovery intents persisted before a crate
// object is uploaded. Referenced immutable objects are never reclaimed.
type NativeCargoMaintenance struct {
	Store interface {
		repository.NativeCargoStore
		repository.LifecycleJobStore
	}
	Objects        OCIObjectStore
	RecoveryWindow time.Duration
	Now            func() time.Time
	Metrics        repository.BackgroundOperationMetrics
}

type cargoReclaimPayload struct {
	Format       repository.Format `json:"format"`
	ObjectKey    string            `json:"objectKey"`
	Tombstone    bool              `json:"tombstone,omitempty"`
	TombstonedAt time.Time         `json:"tombstonedAt,omitempty"`
}

func (m NativeCargoMaintenance) Collect(ctx context.Context) error {
	now := time.Now().UTC()
	if m.Now != nil {
		now = m.Now().UTC()
	}
	window := m.RecoveryWindow
	if window <= 0 {
		window = 24 * time.Hour
	}
	if err := m.EnqueueReclaimJobs(ctx, now.Add(-window), 200); err != nil {
		return err
	}
	return m.RunReclaimJobs(ctx, 200)
}

func (m NativeCargoMaintenance) EnqueueReclaimJobs(ctx context.Context, before time.Time, limit int) error {
	objects, err := m.Store.ListReclaimableCargoObjects(ctx, before, limit, "")
	if err != nil {
		return err
	}
	for _, object := range objects {
		payload, err := json.Marshal(cargoReclaimPayload{Format: repository.FormatCargo, ObjectKey: object.ObjectKey,
			Tombstone: true, TombstonedAt: object.TombstonedAt})
		if err != nil {
			return err
		}
		_, _, err = m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: object.RepositoryID,
			Kind: repository.LifecycleJobReclaim, IdempotencyKey: repository.CargoTombstoneReclaimKey(object.ObjectKey, object.TombstonedAt), Payload: payload})
		if err != nil && !errors.Is(err, repository.ErrIdempotencyConflict) {
			return err
		}
	}
	return nil
}

func (m NativeCargoMaintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatCargo, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		m.begin()
		var payload cargoReclaimPayload
		if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != repository.FormatCargo || payload.ObjectKey == "" ||
			(payload.Tombstone && payload.TombstonedAt.IsZero()) {
			failErr := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "invalid Cargo reclaim payload")
			if firstErr == nil {
				firstErr = errors.New("invalid Cargo reclaim payload")
				if failErr != nil {
					firstErr = failErr
				}
			}
			m.end("failed")
			continue
		}
		release, lockErr := m.Store.LockCargoObject(ctx, payload.ObjectKey)
		if lockErr != nil {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "Cargo object coordination failed")
			if firstErr == nil {
				firstErr = lockErr
			}
			m.end("failed")
			continue
		}
		jobErr := m.reclaim(ctx, job, payload)
		release()
		if jobErr != nil {
			m.end("failed")
		} else {
			m.end("completed")
		}
		if jobErr != nil && firstErr == nil {
			firstErr = jobErr
		}
	}
	return firstErr
}

func (m NativeCargoMaintenance) begin() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatCargo, "started")
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatCargo, 1)
	}
}

func (m NativeCargoMaintenance) end(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatCargo, outcome)
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatCargo, -1)
	}
}

func (m NativeCargoMaintenance) reclaim(ctx context.Context, job repository.LifecycleJob, payload cargoReclaimPayload) error {
	if payload.Tombstone {
		matches, err := m.Store.CargoObjectMatchesTombstone(ctx, payload.ObjectKey, payload.TombstonedAt)
		if err != nil {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "Cargo tombstone generation lookup failed")
			return err
		}
		if !matches {
			return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
		}
		if err := m.Store.MarkCargoObjectCollecting(ctx, payload.ObjectKey); err != nil {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "mark Cargo object collecting failed")
			return err
		}
		visible, err := m.Store.CargoObjectHasVisibleReference(ctx, payload.ObjectKey)
		if err != nil {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "Cargo object visible-reference lookup failed")
			return err
		}
		if !visible {
			if m.Objects == nil {
				_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "Cargo object store is unavailable")
				return errors.New("Cargo object store is unavailable")
			}
			if err := m.Objects.Delete(ctx, payload.ObjectKey); err != nil && !errors.Is(err, objectstore.ErrNotFound) {
				_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "delete tombstoned Cargo object failed")
				return err
			}
		}
		if err := m.Store.MarkCargoObjectCollected(ctx, payload.ObjectKey); err != nil {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "mark Cargo object collected failed")
			return err
		}
		return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
	}
	referenced, err := m.Store.CargoObjectHasReference(ctx, payload.ObjectKey)
	if err != nil {
		_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "Cargo object reference lookup failed")
		return err
	}
	if !referenced {
		err = m.Objects.Delete(ctx, payload.ObjectKey)
		if err != nil && !errors.Is(err, objectstore.ErrNotFound) {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "delete Cargo publication object failed")
			return err
		}
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func (m NativeCargoMaintenance) StartScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		window := m.RecoveryWindow
		if window <= 0 {
			window = 24 * time.Hour
		}
		now := time.Now
		if m.Now != nil {
			now = m.Now
		}
		_ = m.EnqueueReclaimJobs(ctx, now().UTC().Add(-window), 200)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.EnqueueReclaimJobs(ctx, now().UTC().Add(-window), 200)
			}
		}
	}()
}

func (m NativeCargoMaintenance) StartWorker(ctx context.Context, interval time.Duration) {
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
