package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type NativePromotion struct {
	Store interface {
		repository.NativeNPMStore
		repository.LifecycleJobStore
	}
	Intelligence repository.ArtifactIntelligenceStore
	Metrics      repository.BackgroundOperationMetrics
}

type PromotionPayload struct {
	Format             repository.Format `json:"format"`
	SourceRepositoryID string            `json:"sourceRepositoryId"`
	PackageName        string            `json:"packageName"`
	Version            string            `json:"version"`
	Digest             string            `json:"digest"`
}

func (m NativePromotion) Enqueue(ctx context.Context, targetID, key string, payload PromotionPayload) (repository.LifecycleJob, bool, error) {
	payload.Format = repository.FormatNPM
	body, err := json.Marshal(payload)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: targetID, Kind: repository.LifecycleJobPromotion, IdempotencyKey: key, Payload: body})
}

func (m NativePromotion) RunJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobPromotion, repository.FormatNPM, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		if err = m.run(ctx, job); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m NativePromotion) run(ctx context.Context, job repository.LifecycleJob) error {
	var payload PromotionPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != repository.FormatNPM || payload.SourceRepositoryID == "" || !ValidPackageName(payload.PackageName) || !ValidVersion(payload.Version) || payload.Digest == "" {
		return m.fail(ctx, job, "invalid npm promotion payload")
	}
	operationCtx, release, err := repository.LockNPMProxyWithContext(ctx, m.Store, "promotion:"+job.RepositoryID+":"+payload.PackageName+":"+payload.Version)
	if err != nil {
		return m.fail(ctx, job, "target npm version coordination failed")
	}
	defer release()
	source, err := m.Store.GetNPMVersion(operationCtx, payload.SourceRepositoryID, payload.PackageName, payload.Version)
	if err != nil || source.Digest != payload.Digest || source.ObjectKey == "" {
		return m.fail(ctx, job, "source npm version is unavailable")
	}
	objectCtx, objectRelease, err := repository.LockObjectKeys(operationCtx, []string{source.ObjectKey}, m.Store, repository.FormatNPM, m.Store.LockNPMObject)
	if err != nil {
		return m.fail(ctx, job, "target npm object coordination failed")
	}
	defer objectRelease()
	if existing, lookupErr := m.Store.GetNPMVersion(objectCtx, job.RepositoryID, payload.PackageName, payload.Version); lookupErr == nil {
		if existing.Digest == source.Digest && existing.ObjectKey == source.ObjectKey {
			return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
		}
		return m.fail(ctx, job, "target npm version already exists")
	} else if !errors.Is(lookupErr, repository.ErrNotFound) {
		return m.fail(ctx, job, "target npm version lookup failed")
	}
	pkg, err := m.Store.GetNPMPackage(objectCtx, payload.SourceRepositoryID, payload.PackageName)
	if err != nil {
		return m.fail(ctx, job, "source npm package is unavailable")
	}
	tags := make(map[string]string)
	for tag, version := range pkg.DistTags {
		if version == payload.Version {
			tags[tag] = version
		}
	}
	coordinate := payload.PackageName + "@" + payload.Version
	releaseAdmission, err := repository.LockArtifactDistributionAdmission(objectCtx, m.Store, payload.SourceRepositoryID, repository.FormatNPM, coordinate, payload.Digest)
	if errors.Is(err, repository.ErrArtifactQuarantined) {
		return m.fail(ctx, job, repository.ArtifactQuarantinedReason)
	}
	if err != nil {
		return m.fail(ctx, job, "evaluate npm artifact quarantine failed")
	}
	source.RepositoryID = job.RepositoryID
	if _, err = m.Store.PublishNPMVersion(objectCtx, source, tags); err != nil {
		releaseAdmission()
		return m.fail(ctx, job, "publish target npm version failed")
	}
	releaseAdmission()
	intelligenceErr := repository.CopyArtifactIntelligenceOrEnqueue(ctx, m.Intelligence, m.Store, job.RepositoryID, payload.SourceRepositoryID, repository.FormatNPM, coordinate, payload.Digest)
	if intelligenceErr != nil && !errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) {
		return m.fail(ctx, job, fmt.Sprintf("copy npm artifact intelligence failed: %v", intelligenceErr))
	}
	if errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) && m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("intelligence-copy", repository.FormatNPM, "deferred")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func (m NativePromotion) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message)
	return fmt.Errorf("%s", message)
}

func (m NativePromotion) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = m.RunJobs(ctx, 100)
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
