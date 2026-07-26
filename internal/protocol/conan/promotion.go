package conan

import (
	"context"
	"encoding/json"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type NativePromotion struct {
	Store interface {
		repository.NativeConanStore
		repository.LifecycleJobStore
	}
}
type PromotionPayload struct {
	Format             repository.Format `json:"format"`
	SourceRepositoryID string            `json:"sourceRepositoryId"`
	Reference          string            `json:"reference"`
	Revision           string            `json:"revision"`
	Digest             string            `json:"digest"`
}

func (m NativePromotion) Enqueue(ctx context.Context, target, key string, p PromotionPayload) (repository.LifecycleJob, bool, error) {
	p.Format = repository.FormatConan
	body, err := json.Marshal(p)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: target, Kind: repository.LifecycleJobPromotion, IdempotencyKey: key, Payload: body})
}
func (m NativePromotion) RunJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobPromotion, repository.FormatConan, limit)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		var p PromotionPayload
		if err = json.Unmarshal(job.Payload, &p); err != nil || p.Format != repository.FormatConan || p.SourceRepositoryID == "" || p.Reference == "" || p.Revision == "" || p.Digest == "" {
			if e := m.Store.FailLifecycleJob(ctx, job.ID, "invalid Conan promotion payload"); e != nil {
				return e
			}
			continue
		}
		if _, err = m.Store.PromoteConanRecipeRevision(ctx, repository.ConanPromotion{SourceRepositoryID: p.SourceRepositoryID, TargetRepositoryID: job.RepositoryID, Reference: p.Reference, Revision: p.Revision, Digest: p.Digest}); err != nil {
			if e := m.Store.FailLifecycleJob(ctx, job.ID, "promote Conan recipe failed"); e != nil {
				return e
			}
			continue
		}
		if err = m.Store.CompleteLifecycleJob(ctx, job.ID); err != nil {
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
