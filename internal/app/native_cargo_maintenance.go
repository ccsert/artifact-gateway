package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// NativeCargoMaintenance completes recovery intents persisted before a crate
// object is uploaded. Referenced immutable objects are never reclaimed.
type NativeCargoMaintenance struct {
	Store interface {
		repository.NativeCargoStore
		repository.LifecycleJobStore
	}
	Objects OCIObjectStore
}

type cargoReclaimPayload struct {
	Format    repository.Format `json:"format"`
	ObjectKey string            `json:"objectKey"`
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
		var payload cargoReclaimPayload
		if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != repository.FormatCargo || payload.ObjectKey == "" {
			failErr := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "invalid Cargo reclaim payload")
			if firstErr == nil {
				firstErr = errors.New("invalid Cargo reclaim payload")
				if failErr != nil {
					firstErr = failErr
				}
			}
			continue
		}
		release, lockErr := m.Store.LockCargoObject(ctx, payload.ObjectKey)
		if lockErr != nil {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "Cargo object coordination failed")
			if firstErr == nil {
				firstErr = lockErr
			}
			continue
		}
		jobErr := m.reclaim(ctx, job, payload)
		release()
		if jobErr != nil && firstErr == nil {
			firstErr = jobErr
		}
	}
	return firstErr
}

func (m NativeCargoMaintenance) reclaim(ctx context.Context, job repository.LifecycleJob, payload cargoReclaimPayload) error {
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
