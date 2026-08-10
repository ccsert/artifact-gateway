package conan

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type NativePromotion struct {
	Store interface {
		repository.NativeConanStore
		repository.LifecycleJobStore
	}
	Intelligence repository.ArtifactIntelligenceStore
	Metrics      repository.BackgroundOperationMetrics
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
	var firstErr error
	for _, job := range jobs {
		m.begin()
		var p PromotionPayload
		if err = json.Unmarshal(job.Payload, &p); err != nil || p.Format != repository.FormatConan || p.SourceRepositoryID == "" || p.Reference == "" || p.Revision == "" || p.Digest == "" {
			if e := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "invalid Conan promotion payload"); e != nil {
				m.end("failed")
				if firstErr == nil {
					firstErr = e
				}
				continue
			}
			m.end("failed")
			continue
		}
		if _, err = m.Store.PromoteConanRecipeRevision(ctx, repository.ConanPromotion{SourceRepositoryID: p.SourceRepositoryID, TargetRepositoryID: job.RepositoryID, Reference: p.Reference, Revision: p.Revision, Digest: p.Digest}); err != nil {
			if e := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "promote Conan recipe failed"); e != nil {
				m.end("failed")
				if firstErr == nil {
					firstErr = e
				}
				continue
			}
			m.end("failed")
			continue
		}
		intelligenceErr := repository.CopyArtifactIntelligenceOrEnqueue(ctx, m.Intelligence, m.Store, job.RepositoryID, p.SourceRepositoryID, repository.FormatConan, p.Reference+"#"+p.Revision, p.Digest)
		if intelligenceErr != nil && !errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) {
			if e := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "copy Conan artifact intelligence failed"); e != nil && firstErr == nil {
				firstErr = e
			}
			m.end("failed")
			continue
		}
		if errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) && m.Metrics != nil {
			m.Metrics.RecordBackgroundOperation("intelligence-copy", repository.FormatConan, "deferred")
		}
		if err = m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
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
func (m NativePromotion) begin() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("promotion", repository.FormatConan, "started")
		m.Metrics.AddBackgroundOperationInFlight("promotion", repository.FormatConan, 1)
	}
}
func (m NativePromotion) end(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("promotion", repository.FormatConan, outcome)
		m.Metrics.AddBackgroundOperationInFlight("promotion", repository.FormatConan, -1)
	}
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
