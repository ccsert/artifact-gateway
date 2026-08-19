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

// NativeGoMaintenance executes both publication-orphan recovery intents and
// delayed reclaim for tombstoned Hosted Go versions. Object locks keep both
// paths serialized with publication and restore.
type NativeGoMaintenance struct {
	Store interface {
		repository.NativeGoStore
		repository.LifecycleJobStore
	}
	Objects        OCIObjectStore
	RecoveryWindow time.Duration
	Now            func() time.Time
	Metrics        repository.BackgroundOperationMetrics
}

type goReclaimPayload struct {
	Format       repository.Format `json:"format"`
	ObjectKey    string            `json:"objectKey"`
	Tombstone    bool              `json:"tombstone,omitempty"`
	TombstonedAt time.Time         `json:"tombstonedAt,omitempty"`
}

func (m NativeGoMaintenance) Collect(ctx context.Context) error {
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

func (m NativeGoMaintenance) EnqueueReclaimJobs(ctx context.Context, before time.Time, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	objects, err := m.Store.ListReclaimableGoModuleObjects(ctx, before, limit, "")
	if err != nil {
		return err
	}
	for _, object := range objects {
		payload, _ := json.Marshal(goReclaimPayload{Format: repository.FormatGo, ObjectKey: object.ObjectKey, Tombstone: true, TombstonedAt: object.TombstonedAt})
		key := "go-tombstone-object:" + object.ObjectKey + ":" + object.TombstonedAt.UTC().Format(time.RFC3339Nano)
		if _, _, err = m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: object.RepositoryID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: key, Payload: payload}); err != nil && !errors.Is(err, repository.ErrIdempotencyConflict) {
			return err
		}
	}
	return nil
}

func (m NativeGoMaintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	var firstErr error
	remaining := limit
	for remaining > 0 {
		jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatGo, remaining)
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			break
		}
		remaining -= len(jobs)
		for _, job := range jobs {
			m.begin()
			var payload goReclaimPayload
			if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != repository.FormatGo || payload.ObjectKey == "" || (payload.Tombstone && payload.TombstonedAt.IsZero()) {
				jobErr := m.fail(ctx, job, "invalid Go reclaim payload")
				if firstErr == nil {
					firstErr = jobErr
				}
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
	}
	return firstErr
}

func (m NativeGoMaintenance) runReclaimJob(ctx context.Context, job repository.LifecycleJob, payload goReclaimPayload) error {
	if payload.Tombstone {
		return m.runTombstoneReclaimJob(ctx, job, payload)
	}
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

func (m NativeGoMaintenance) runTombstoneReclaimJob(ctx context.Context, job repository.LifecycleJob, payload goReclaimPayload) error {
	matches, err := m.Store.GoModuleObjectMatchesTombstone(ctx, payload.ObjectKey, payload.TombstonedAt)
	if err != nil {
		jobErr := m.fail(ctx, job, "Go object tombstone-generation lookup failed")
		m.end("failed")
		return jobErr
	}
	if !matches {
		if err = m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
			m.end("failed")
			return err
		}
		m.end("completed")
		return nil
	}
	if err = m.Store.MarkGoModuleObjectCollecting(ctx, payload.ObjectKey); err != nil {
		jobErr := m.fail(ctx, job, "mark Go object collecting failed")
		m.end("failed")
		return jobErr
	}
	referenced, err := m.Store.GoModuleObjectHasVisibleReference(ctx, payload.ObjectKey)
	if err != nil {
		jobErr := m.fail(ctx, job, "Go object visible-reference lookup failed")
		m.end("failed")
		return jobErr
	}
	if !referenced && m.Objects == nil {
		jobErr := m.fail(ctx, job, "Go object store is unavailable")
		m.end("failed")
		return jobErr
	}
	if !referenced {
		err = m.Objects.Delete(ctx, payload.ObjectKey)
	}
	if err != nil {
		jobErr := m.fail(ctx, job, fmt.Sprintf("delete tombstoned Go object: %v", err))
		m.end("failed")
		return jobErr
	}
	if err = m.Store.MarkGoModuleObjectCollected(ctx, payload.ObjectKey); err != nil {
		jobErr := m.fail(ctx, job, "mark Go object collected failed")
		m.end("failed")
		return jobErr
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

func (m NativeGoMaintenance) StartScheduler(ctx context.Context, interval time.Duration) {
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
