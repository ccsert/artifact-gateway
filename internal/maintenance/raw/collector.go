package raw

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// Store is the lifecycle data required to safely collect a Raw object.
type Store interface {
	repository.LifecycleJobStore
	ListUnreferencedRawObjects(context.Context, time.Time, int) ([]repository.RawObject, error)
	LockRawObject(context.Context, string) (func(), error)
	RawObjectIsUnreferenced(context.Context, string) (bool, error)
	MarkRawObjectCollected(context.Context, string) error
}

type reclaimPayload struct {
	Format    repository.Format `json:"format"`
	Digest    string            `json:"digest"`
	ObjectKey string            `json:"objectKey"`
}

// ObjectStore deletes content-addressed bytes after the Store has confirmed
// that no Raw asset still references them.
type ObjectStore interface {
	Delete(context.Context, string) error
}

// Collector removes Raw objects that have remained unreferenced throughout the
// retention window. It leaves the metadata collection trace intact.
type Collector struct {
	Store   Store
	Objects ObjectStore
	Now     func() time.Time
	Metrics repository.BackgroundOperationMetrics
}

func (c Collector) Collect(ctx context.Context) error {
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if err := c.EnqueueReclaimJobs(ctx, now().UTC().Add(-24*time.Hour), 100); err != nil {
		return err
	}
	return c.RunReclaimJobs(ctx, 100)
}

func (c Collector) EnqueueReclaimJobs(ctx context.Context, before time.Time, limit int) error {
	objects, err := c.Store.ListUnreferencedRawObjects(ctx, before, limit)
	if err != nil {
		return err
	}
	for _, object := range objects {
		payload, err := json.Marshal(reclaimPayload{Format: repository.FormatRaw, Digest: object.Digest, ObjectKey: object.ObjectKey})
		if err != nil {
			return err
		}
		if _, _, err = c.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: object.RepositoryID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "raw-object:" + object.Digest, Payload: payload}); err != nil {
			return err
		}
	}
	return nil
}

func (c Collector) RunReclaimJobs(ctx context.Context, limit int) error {
	jobs, err := c.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatRaw, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		c.begin()
		if err := c.runReclaimJob(ctx, job); err != nil {
			c.end("failed")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		c.end("completed")
	}
	return firstErr
}

func (c Collector) begin() {
	if c.Metrics != nil {
		c.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatRaw, "started")
		c.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatRaw, 1)
	}
}

func (c Collector) end(outcome string) {
	if c.Metrics != nil {
		c.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatRaw, outcome)
		c.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatRaw, -1)
	}
}

func (c Collector) runReclaimJob(ctx context.Context, job repository.LifecycleJob) error {
	var payload reclaimPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Digest == "" || payload.ObjectKey == "" {
		return c.failReclaimJob(ctx, job, "invalid Raw reclaim payload")
	}
	release, err := c.Store.LockRawObject(ctx, payload.Digest)
	if err != nil {
		return c.failReclaimJob(ctx, job, "Raw object coordination failed")
	}
	defer release()
	unreferenced, err := c.Store.RawObjectIsUnreferenced(ctx, payload.Digest)
	if err != nil {
		return c.failReclaimJob(ctx, job, "Raw object reference lookup failed")
	}
	if !unreferenced {
		return c.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
	}
	if err = c.Objects.Delete(ctx, payload.ObjectKey); err != nil {
		return c.failReclaimJob(ctx, job, fmt.Sprintf("delete Raw object: %v", err))
	}
	if err = c.Store.MarkRawObjectCollected(ctx, payload.Digest); err != nil && err != repository.ErrNotFound {
		return c.failReclaimJob(ctx, job, "mark Raw object collected failed")
	}
	return c.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func (c Collector) failReclaimJob(ctx context.Context, job repository.LifecycleJob, message string) error {
	if err := c.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func (c Collector) Start(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = c.Collect(ctx)
			}
		}
	}()
}
