package raw

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// NativePromotion shares a content-addressed Raw object by adding an immutable
// target asset. Raw reclamation checks assets across repositories, so the new
// reference fences source collection without copying bytes.
type NativePromotion struct {
	Store interface {
		repository.NativeRawStore
		repository.LifecycleJobStore
	}
}
type PromotionPayload struct {
	Format             repository.Format `json:"format"`
	SourceRepositoryID string            `json:"sourceRepositoryId"`
	Path               string            `json:"path"`
	Digest             string            `json:"digest"`
}

func (m NativePromotion) Enqueue(ctx context.Context, targetID, key string, p PromotionPayload) (repository.LifecycleJob, bool, error) {
	p.Format = repository.FormatRaw
	body, err := json.Marshal(p)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: targetID, Kind: repository.LifecycleJobPromotion, IdempotencyKey: key, Payload: body})
}
func (m NativePromotion) RunJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobPromotion, repository.FormatRaw, limit)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := m.run(ctx, job); err != nil {
			return err
		}
	}
	return nil
}
func (m NativePromotion) Start(ctx context.Context, interval time.Duration) {
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
				_ = m.RunJobs(ctx, 100)
			}
		}
	}()
}
func (m NativePromotion) run(ctx context.Context, job repository.LifecycleJob) error {
	var p PromotionPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.Format != repository.FormatRaw || p.SourceRepositoryID == "" || p.Path == "" || p.Digest == "" {
		return m.fail(ctx, job.ID, "invalid Raw promotion payload")
	}
	// Serialize the destination name, not the source digest: separate sources
	// must never race into an immutable target path.
	release, err := m.Store.LockRawObject(ctx, "promotion:"+job.RepositoryID+":"+strings.TrimPrefix(p.Path, "/"))
	if err != nil {
		return m.fail(ctx, job.ID, "target Raw path coordination failed")
	}
	defer release()
	if _, err := m.Store.GetRawAsset(ctx, job.RepositoryID, p.Path); err == nil {
		return m.fail(ctx, job.ID, "target Raw path already exists")
	} else if err != repository.ErrNotFound {
		return m.fail(ctx, job.ID, "target Raw path lookup failed")
	}
	source, err := m.Store.GetRawAsset(ctx, p.SourceRepositoryID, p.Path)
	if err != nil || source.Digest != p.Digest {
		return m.fail(ctx, job.ID, "source Raw asset is unavailable")
	}
	source.RepositoryID = job.RepositoryID
	if _, err = m.Store.PutRawAsset(ctx, source); err != nil {
		return m.fail(ctx, job.ID, "publish target Raw asset failed")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID)
}
func (m NativePromotion) fail(ctx context.Context, id, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, id, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}
