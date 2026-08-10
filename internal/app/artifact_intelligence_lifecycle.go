package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// ArtifactIntelligenceCopyWorker completes metadata propagation that could
// not be performed inline after a successful promotion.
type ArtifactIntelligenceCopyWorker struct {
	Store interface {
		repository.ArtifactIntelligenceStore
		repository.LifecycleJobStore
	}
	WorkerFormats []string
	Metrics       repository.BackgroundOperationMetrics
}

func (w ArtifactIntelligenceCopyWorker) RunJobs(ctx context.Context, limit int) error {
	formats := append([]string(nil), w.WorkerFormats...)
	if len(formats) == 0 {
		for _, format := range repository.WorkerFormats() {
			formats = append(formats, string(format))
		}
	}
	if limit <= 0 {
		limit = 100
	}
	var firstErr error
	remaining := limit
	for _, format := range formats {
		if remaining == 0 {
			break
		}
		jobs, err := w.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobIntelligence, repository.Format(format), remaining)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		remaining -= len(jobs)
		for _, job := range jobs {
			if err := w.run(ctx, job); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (w ArtifactIntelligenceCopyWorker) run(ctx context.Context, job repository.LifecycleJob) error {
	var payload repository.ArtifactIntelligenceCopyPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Format == "" || payload.SourceRepositoryID == "" || payload.Coordinate == "" || payload.Digest == "" {
		return w.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "invalid artifact intelligence copy payload")
	}
	if err := repository.CopyArtifactIntelligence(ctx, w.Store, job.RepositoryID, payload.SourceRepositoryID, payload.Format, payload.Coordinate, payload.Digest); err != nil {
		return w.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, fmt.Sprintf("copy artifact intelligence failed: %v", err))
	}
	if err := w.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
		return err
	}
	if w.Metrics != nil {
		w.Metrics.RecordBackgroundOperation("intelligence-copy", payload.Format, "completed")
	}
	return nil
}

func (w ArtifactIntelligenceCopyWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = w.RunJobs(ctx, 100)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = w.RunJobs(ctx, 100)
			}
		}
	}()
}
