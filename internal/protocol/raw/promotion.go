package raw

import (
	"context"
	"encoding/json"
	"errors"
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
	Intelligence repository.ArtifactIntelligenceStore
	Metrics      repository.BackgroundOperationMetrics
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
	var firstErr error
	for _, job := range jobs {
		m.begin()
		if err := m.run(ctx, job); err != nil {
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
		m.Metrics.RecordBackgroundOperation("promotion", repository.FormatRaw, "started")
		m.Metrics.AddBackgroundOperationInFlight("promotion", repository.FormatRaw, 1)
	}
}
func (m NativePromotion) end(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("promotion", repository.FormatRaw, outcome)
		m.Metrics.AddBackgroundOperationInFlight("promotion", repository.FormatRaw, -1)
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
func (m NativePromotion) run(ctx context.Context, job repository.LifecycleJob) error {
	var p PromotionPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil || p.Format != repository.FormatRaw || p.SourceRepositoryID == "" || p.Path == "" || p.Digest == "" {
		return m.fail(ctx, job, "invalid Raw promotion payload")
	}
	// Serialize the destination name, not the source digest: separate sources
	// must never race into an immutable target path.
	objectCtx, release, err := repository.LockObjectKeys(ctx, []string{"promotion:" + job.RepositoryID + ":" + strings.TrimPrefix(p.Path, "/")}, m.Store, repository.FormatRaw, m.Store.LockRawObject)
	if err != nil {
		return m.fail(ctx, job, "target Raw path coordination failed")
	}
	defer release()
	if _, err := m.Store.GetRawAsset(objectCtx, job.RepositoryID, p.Path); err == nil {
		return m.fail(ctx, job, "target Raw path already exists")
	} else if err != repository.ErrNotFound {
		return m.fail(ctx, job, "target Raw path lookup failed")
	}
	source, err := m.Store.GetRawAsset(objectCtx, p.SourceRepositoryID, p.Path)
	if err != nil || source.Digest != p.Digest {
		return m.fail(ctx, job, "source Raw asset is unavailable")
	}
	releaseAdmission, err := repository.LockArtifactDistributionAdmission(objectCtx, m.Store, p.SourceRepositoryID, repository.FormatRaw, p.Path, p.Digest)
	if errors.Is(err, repository.ErrArtifactQuarantined) {
		return m.fail(ctx, job, repository.ArtifactQuarantinedReason)
	}
	if err != nil {
		return m.fail(ctx, job, "evaluate Raw artifact quarantine failed")
	}
	defer releaseAdmission()
	source.RepositoryID = job.RepositoryID
	if _, err = m.Store.PutRawAsset(objectCtx, source); err != nil {
		return m.fail(ctx, job, "publish target Raw asset failed")
	}
	intelligenceErr := repository.CopyArtifactIntelligenceOrEnqueue(ctx, m.Intelligence, m.Store, job.RepositoryID, p.SourceRepositoryID, repository.FormatRaw, p.Path, p.Digest)
	if intelligenceErr != nil && !errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) {
		return m.fail(ctx, job, fmt.Sprintf("copy Raw artifact intelligence failed: %v", intelligenceErr))
	}
	if errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) && m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("intelligence-copy", repository.FormatRaw, "deferred")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}
func (m NativePromotion) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}
