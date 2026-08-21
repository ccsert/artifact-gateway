package raw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// Store is the lifecycle data required to safely collect a Raw object.
type Store interface {
	repository.LifecycleJobStore
	ExpireRawUploads(context.Context, time.Time, int) ([]repository.RawUpload, error)
	ListUncollectedRawUploads(context.Context, int) ([]repository.RawUpload, error)
	LockRawUpload(context.Context, string) (func(), error)
	GetRawUpload(context.Context, string) (repository.RawUpload, error)
	MarkRawUploadCollected(context.Context, string) error
	ListUnreferencedRawObjects(context.Context, time.Time, int) ([]repository.RawObject, error)
	LockRawObject(context.Context, string) (func(), error)
	RawObjectIsUnreferenced(context.Context, string) (bool, error)
	MarkRawObjectCollected(context.Context, string) error
}

type reclaimPayload struct {
	Format    repository.Format `json:"format"`
	Digest    string            `json:"digest"`
	ObjectKey string            `json:"objectKey"`
	UploadID  string            `json:"uploadId,omitempty"`
}

// ObjectStore lists and deletes upload staging or content-addressed bytes after
// the Store has confirmed they are no longer visible or writable.
type ObjectStore interface {
	Delete(context.Context, string) error
	List(context.Context, string) ([]string, error)
}

// Collector removes expired Raw upload staging and objects that have remained
// unreferenced throughout the retention window. It leaves a metadata trace.
type Collector struct {
	Store   Store
	Objects ObjectStore
	Now     func() time.Time
	Metrics repository.BackgroundOperationMetrics
}

func (c Collector) Collect(ctx context.Context) error {
	if err := c.Schedule(ctx); err != nil {
		return err
	}
	return c.RunReclaimJobs(ctx, 100)
}

// Schedule records durable cleanup work for expired upload staging and
// unreferenced published objects without touching the object store.
func (c Collector) Schedule(ctx context.Context) error {
	now := c.now()
	if _, err := c.Store.ExpireRawUploads(ctx, now, 100); err != nil {
		return err
	}
	uploads, err := c.Store.ListUncollectedRawUploads(ctx, 100)
	if err != nil {
		return err
	}
	for _, upload := range uploads {
		payload, err := json.Marshal(reclaimPayload{Format: repository.FormatRaw, ObjectKey: upload.ObjectKey, UploadID: upload.ID})
		if err != nil {
			return err
		}
		if _, _, err = c.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
			ID: uuid.NewString(), RepositoryID: upload.RepositoryID, Kind: repository.LifecycleJobReclaim,
			IdempotencyKey: "raw-upload:" + upload.ID, Payload: payload,
		}); err != nil {
			return err
		}
	}
	return c.EnqueueReclaimJobs(ctx, now.Add(-24*time.Hour), 100)
}

func (c Collector) now() time.Time {
	if c.Now != nil {
		return c.Now().UTC()
	}
	return time.Now().UTC()
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
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ObjectKey == "" {
		return c.failReclaimJob(ctx, job, "invalid Raw reclaim payload")
	}
	if payload.UploadID != "" {
		return c.runUploadReclaimJob(ctx, job, payload)
	}
	if payload.Digest == "" {
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

func (c Collector) runUploadReclaimJob(ctx context.Context, job repository.LifecycleJob, payload reclaimPayload) error {
	release, err := c.Store.LockRawUpload(ctx, payload.UploadID)
	if err != nil {
		return c.failReclaimJob(ctx, job, "Raw upload coordination failed")
	}
	defer release()
	upload, err := c.Store.GetRawUpload(ctx, payload.UploadID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return c.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
		}
		return c.failReclaimJob(ctx, job, "Raw upload lookup failed")
	}
	if upload.State == "open" || !upload.CollectedAt.IsZero() {
		return c.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
	}
	if upload.ObjectKey != payload.ObjectKey {
		return c.failReclaimJob(ctx, job, "Raw upload reclaim payload does not match upload")
	}
	if err = deleteUploadObjects(ctx, c.Objects, payload.ObjectKey); err != nil {
		return c.failReclaimJob(ctx, job, fmt.Sprintf("delete Raw upload object: %v", err))
	}
	if err = c.Store.MarkRawUploadCollected(ctx, payload.UploadID); err != nil {
		return c.failReclaimJob(ctx, job, "mark Raw upload collected failed")
	}
	return c.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func deleteUploadObjects(ctx context.Context, objects ObjectStore, key string) error {
	parts, err := objects.List(ctx, key+".parts/")
	deleteErr := err
	if err == nil {
		for _, partKey := range parts {
			deleteErr = errors.Join(deleteErr, objects.Delete(ctx, partKey))
		}
	}
	return errors.Join(deleteErr, objects.Delete(ctx, key))
}

func (c Collector) failReclaimJob(ctx context.Context, job repository.LifecycleJob, message string) error {
	if err := c.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func (c Collector) Start(ctx context.Context, interval time.Duration) {
	c.StartScheduler(ctx, interval)
	c.StartWorker(ctx, interval)
}

// StartScheduler discovers expired Raw uploads and unreferenced objects, then
// records durable reclaim jobs. It does not touch object-store bytes.
func (c Collector) StartScheduler(ctx context.Context, interval time.Duration) {
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
				_ = c.Schedule(ctx)
			}
		}
	}()
}

// StartWorker only claims and executes Raw reclaim jobs.
func (c Collector) StartWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = c.RunReclaimJobs(ctx, 100)
		wake := notificationWake(ctx, c.Store, "artifact_gateway_lifecycle_jobs")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = c.RunReclaimJobs(ctx, 100)
			case <-wake:
				_ = c.RunReclaimJobs(ctx, 100)
			}
		}
	}()
}

type postgresNotificationSource interface {
	Listen(context.Context, string) <-chan struct{}
}

func notificationWake(ctx context.Context, store any, channel string) <-chan struct{} {
	if source, ok := store.(postgresNotificationSource); ok {
		return source.Listen(ctx, channel)
	}
	return nil
}
