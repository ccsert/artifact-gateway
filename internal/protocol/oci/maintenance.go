package oci

import (
	"context"
	"encoding/json"
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
}

func (m NativeMaintenance) Collect(ctx context.Context) error {
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
		if err := m.Objects.Delete(ctx, upload.ObjectKey); err != nil {
			return err
		}
		if err := m.Store.MarkOCIUploadCollected(ctx, upload.ID); err != nil {
			return err
		}
	}
	if err := m.EnqueueReclaimJobs(ctx, now().UTC().Add(-24*time.Hour), 100); err != nil {
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
	for _, job := range jobs {
		m.begin()
		if err := m.runReclaimJob(ctx, job); err != nil {
			m.end("failed")
			return err
		}
		m.end("completed")
	}
	return nil
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
		return m.failReclaimJob(ctx, job.ID, "invalid OCI reclaim payload")
	}
	release, err := m.Store.LockOCIObject(ctx, payload.ObjectKey)
	if err != nil {
		return m.failReclaimJob(ctx, job.ID, "OCI object coordination failed")
	}
	defer release()
	unclaimed, err := m.Store.OCIObjectIntentIsUnclaimed(ctx, payload.ObjectKey)
	if err != nil {
		return m.failReclaimJob(ctx, job.ID, "OCI object intent lookup failed")
	}
	if !unclaimed {
		return m.Store.CompleteLifecycleJob(ctx, job.ID)
	}
	if err := m.Objects.Delete(ctx, payload.ObjectKey); err != nil {
		return m.failReclaimJob(ctx, job.ID, fmt.Sprintf("delete OCI object: %v", err))
	}
	if err := m.Store.MarkOCIObjectIntentCollected(ctx, payload.ObjectKey); err != nil {
		return m.failReclaimJob(ctx, job.ID, "mark OCI object intent collected failed")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID)
}

func (m NativeMaintenance) failReclaimJob(ctx context.Context, id, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, id, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func (m NativeMaintenance) Start(ctx context.Context, interval time.Duration) {
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
				_ = m.Collect(ctx)
			}
		}
	}()
}
