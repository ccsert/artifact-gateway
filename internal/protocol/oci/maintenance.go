package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// NativeMaintenance cleans expired native OCI upload and object intents outside
// request handling, preserving their durable lifecycle records for operators.
type NativeMaintenance struct {
	Store   OCIReclaimStore
	Objects objectstore.Store
	Now     func() time.Time
	Metrics repository.BackgroundOperationMetrics
}

// OCIReclaimStore keeps physical object collection scoped to the repository
// that originally staged the object, while preserving native OCI metadata.
type OCIReclaimStore interface {
	repository.NativeOCIStore
	repository.LifecycleJobStore
}

type reclaimPayload struct {
	Format    repository.Format `json:"format"`
	ObjectKey string            `json:"objectKey"`
	UploadID  string            `json:"uploadId,omitempty"`
}

func (m NativeMaintenance) Collect(ctx context.Context) error {
	if err := m.Schedule(ctx); err != nil {
		return err
	}
	return m.RunReclaimJobs(ctx, 100)
}

// EnqueueReclaimJobs turns old unclaimed intents into idempotent lifecycle
// work. It deliberately does no object-store I/O in the scanning phase.
func (m NativeMaintenance) EnqueueReclaimJobs(ctx context.Context, before time.Time, limit int) error {
	intents, err := m.Store.ListUnclaimedOCIObjectIntents(ctx, before, limit)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		payload, err := json.Marshal(reclaimPayload{Format: repository.FormatOCI, ObjectKey: intent.ObjectKey})
		if err != nil {
			return err
		}
		if _, _, err := m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
			ID:             uuid.NewString(),
			RepositoryID:   intent.RepositoryID,
			Kind:           repository.LifecycleJobReclaim,
			IdempotencyKey: "oci-object:" + intent.ObjectKey,
			Payload:        payload,
		}); err != nil {
			return err
		}
	}
	return nil
}

// RunReclaimJobs performs the guarded physical deletion for OCI reclaim jobs.
// Jobs for other lifecycle responsibilities remain pending for their workers.
func (m NativeMaintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatOCI, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		m.begin()
		if err := m.runReclaimJob(ctx, job); err != nil {
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

func (m NativeMaintenance) begin() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatOCI, "started")
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatOCI, 1)
	}
}

func (m NativeMaintenance) end(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatOCI, outcome)
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatOCI, -1)
	}
}

func (m NativeMaintenance) runReclaimJob(ctx context.Context, job repository.LifecycleJob) error {
	var payload reclaimPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ObjectKey == "" {
		return m.failReclaimJob(ctx, job, "invalid OCI reclaim payload")
	}
	if payload.UploadID != "" {
		return m.runUploadReclaimJob(ctx, job, payload)
	}
	release, err := m.Store.LockOCIObject(ctx, payload.ObjectKey)
	if err != nil {
		return m.failReclaimJob(ctx, job, "OCI object coordination failed")
	}
	defer release()
	unclaimed, err := m.Store.OCIObjectIntentIsUnclaimed(ctx, payload.ObjectKey)
	if err != nil {
		return m.failReclaimJob(ctx, job, "OCI object intent lookup failed")
	}
	if !unclaimed {
		return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
	}
	if err := m.Objects.Delete(ctx, payload.ObjectKey); err != nil {
		return m.failReclaimJob(ctx, job, fmt.Sprintf("delete OCI object: %v", err))
	}
	if err := m.Store.MarkOCIObjectIntentCollected(ctx, payload.ObjectKey); err != nil {
		return m.failReclaimJob(ctx, job, "mark OCI object intent collected failed")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func (m NativeMaintenance) runUploadReclaimJob(ctx context.Context, job repository.LifecycleJob, payload reclaimPayload) error {
	release, err := m.Store.LockOCIUpload(ctx, payload.UploadID)
	if err != nil {
		return m.failReclaimJob(ctx, job, "OCI upload coordination failed")
	}
	defer release()
	upload, err := m.Store.GetOCIUpload(ctx, payload.UploadID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
		}
		return m.failReclaimJob(ctx, job, "OCI upload lookup failed")
	}
	if upload.State != "expired" || !upload.CollectedAt.IsZero() {
		return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
	}
	if upload.ObjectKey != payload.ObjectKey {
		return m.failReclaimJob(ctx, job, "OCI upload reclaim payload does not match upload")
	}
	if err = m.Objects.Delete(ctx, payload.ObjectKey); err != nil {
		return m.failReclaimJob(ctx, job, fmt.Sprintf("delete OCI upload object: %v", err))
	}
	if err = m.Store.MarkOCIUploadCollected(ctx, payload.UploadID); err != nil {
		return m.failReclaimJob(ctx, job, "mark OCI upload collected failed")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func (m NativeMaintenance) failReclaimJob(ctx context.Context, job repository.LifecycleJob, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func (m NativeMaintenance) Start(ctx context.Context, interval time.Duration) {
	m.StartScheduler(ctx, interval)
	m.StartWorker(ctx, interval)
}

// Schedule expires abandoned uploads and records durable reclaim jobs without
// touching object-store bytes. Physical cleanup is left to StartWorker.
func (m NativeMaintenance) Schedule(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	if _, err := m.Store.ExpireOCIUploads(ctx, now().UTC(), 100); err != nil {
		return err
	}
	uploads, err := m.Store.ListUncollectedOCIUploads(ctx, 100)
	if err != nil {
		return err
	}
	for _, upload := range uploads {
		payload, err := json.Marshal(reclaimPayload{Format: repository.FormatOCI, ObjectKey: upload.ObjectKey, UploadID: upload.ID})
		if err != nil {
			return err
		}
		if _, _, err = m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: upload.RepositoryID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "oci-upload:" + upload.ID, Payload: payload}); err != nil {
			return err
		}
	}
	return m.EnqueueReclaimJobs(ctx, now().UTC().Add(-24*time.Hour), 100)
}

func (m NativeMaintenance) StartScheduler(ctx context.Context, interval time.Duration) {
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
				_ = m.Schedule(ctx)
			}
		}
	}()
}

func (m NativeMaintenance) StartWorker(ctx context.Context, interval time.Duration) {
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

type notificationSource interface {
	Listen(context.Context, string) <-chan struct{}
}

func notificationWake(ctx context.Context, store any, channel string) <-chan struct{} {
	if source, ok := store.(notificationSource); ok {
		return source.Listen(ctx, channel)
	}
	return nil
}
