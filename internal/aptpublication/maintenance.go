package aptpublication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/lifecycle"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type maintenanceStore interface {
	repository.NativeAPTStore
	repository.NativeAPTPublicationStore
	repository.LifecycleJobStore
}

// Maintenance turns expired pre-visibility publication sessions into durable
// reclaim jobs. Object deletion is fenced by the same content-addressed object
// lock used by UploadPackage and is retried by the lifecycle worker.
type Maintenance struct {
	Store   maintenanceStore
	Objects objectstore.Store
	Now     func() time.Time
	Metrics repository.BackgroundOperationMetrics
}

type reclaimPayload struct {
	Format    repository.Format `json:"format"`
	SessionID string            `json:"sessionId"`
	ObjectKey string            `json:"objectKey"`
}

func (m Maintenance) Collect(ctx context.Context) error {
	if err := m.Schedule(ctx); err != nil {
		return err
	}
	return m.RunReclaimJobs(ctx, 100)
}

// Schedule persists reclaim work only. It never performs object-store I/O.
func (m Maintenance) Schedule(ctx context.Context) error {
	if m.Store == nil || m.Objects == nil {
		return errors.New("APT publication maintenance dependencies are required")
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	if _, err := m.Store.ExpireAPTPublicationSessions(ctx, now().UTC(), 100); err != nil {
		return err
	}
	items, err := m.Store.ListUnscheduledAPTPublicationObjects(ctx, 100)
	if err != nil {
		return err
	}
	for _, item := range items {
		payload, marshalErr := json.Marshal(reclaimPayload{Format: repository.FormatAPT, SessionID: item.SessionID, ObjectKey: item.ObjectKey})
		if marshalErr != nil {
			return marshalErr
		}
		if _, _, err = m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
			ID: uuid.NewString(), RepositoryID: item.RepositoryID, Kind: repository.LifecycleJobReclaim,
			IdempotencyKey: "apt-publication:" + item.SessionID, Payload: payload,
		}); err != nil {
			return err
		}
		if err = m.Store.MarkAPTPublicationObjectScheduled(ctx, item.SessionID, item.ObjectKey); err != nil {
			return err
		}
	}
	return nil
}

func (m Maintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	return m.runtime().RunJobs(ctx, limit, m.runReclaimJob)
}

func (m Maintenance) runReclaimJob(ctx context.Context, job repository.LifecycleJob) error {
	var payload reclaimPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Format != repository.FormatAPT || payload.SessionID == "" || payload.ObjectKey == "" {
		return errors.New("invalid APT publication reclaim payload")
	}
	objectCtx, release, err := repository.LockObjectKeys(ctx, []string{payload.ObjectKey}, m.Store, repository.FormatAPT, m.Store.LockAPTObject)
	if err != nil {
		return errors.New("APT publication object coordination failed")
	}
	defer release()
	referenced, err := m.Store.APTObjectHasPackageReference(objectCtx, payload.ObjectKey)
	if err != nil {
		return errors.New("APT package reference lookup failed")
	}
	if !referenced {
		if err = m.Objects.Delete(objectCtx, payload.ObjectKey); err != nil {
			return fmt.Errorf("delete abandoned APT publication object: %v", err)
		}
	}
	if err = m.Store.MarkAPTPublicationObjectCollected(objectCtx, payload.SessionID, payload.ObjectKey); err != nil && !errors.Is(err, repository.ErrNotFound) {
		return errors.New("mark APT publication object collected failed")
	}
	return nil
}

func (m Maintenance) runtime() lifecycle.Runtime {
	return lifecycle.Runtime{
		Store: m.Store, Kind: repository.LifecycleJobReclaim, Formats: []repository.Format{repository.FormatAPT},
		Name: "APT publication reclaim", Operation: "lifecycle", Metrics: m.Metrics,
		LeaseRefreshInterval: 3 * time.Minute, LeaseProgressMessage: "reclaiming abandoned APT publication object",
	}
}

func (m Maintenance) StartScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = m.Schedule(ctx)
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

func (m Maintenance) StartWorker(ctx context.Context, interval time.Duration) {
	m.runtime().Start(ctx, interval, 100, m.runReclaimJob)
}
