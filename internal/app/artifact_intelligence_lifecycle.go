package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/lifecycle"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// ArtifactIntelligenceCopyWorker completes metadata propagation that could
// not be performed inline after a successful promotion.
type ArtifactIntelligenceCopyWorker struct {
	Store interface {
		repository.ArtifactIntelligenceStore
		lifecycle.Store
	}
	WorkerFormats        []string
	Metrics              repository.BackgroundOperationMetrics
	LeaseRefreshInterval time.Duration
}

func (w ArtifactIntelligenceCopyWorker) RunJobs(ctx context.Context, limit int) error {
	return w.runtime(w.formats()).RunJobs(ctx, limit, w.execute)
}

func (w ArtifactIntelligenceCopyWorker) formats() []repository.Format {
	formatNames := append([]string(nil), w.WorkerFormats...)
	if len(formatNames) == 0 {
		for _, format := range repository.WorkerFormats() {
			formatNames = append(formatNames, string(format))
		}
	}
	formats := make([]repository.Format, 0, len(formatNames))
	for _, format := range formatNames {
		formats = append(formats, repository.Format(format))
	}
	return formats
}

func (w ArtifactIntelligenceCopyWorker) execute(ctx context.Context, job repository.LifecycleJob) error {
	var payload repository.ArtifactIntelligenceCopyPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Format == "" || payload.SourceRepositoryID == "" || payload.Coordinate == "" || payload.Digest == "" {
		return errors.New("invalid artifact intelligence copy payload")
	}
	if err := repository.CopyArtifactIntelligence(ctx, w.Store, job.RepositoryID, payload.SourceRepositoryID, payload.Format, payload.Coordinate, payload.Digest); err != nil {
		return fmt.Errorf("copy artifact intelligence failed: %v", err)
	}
	return nil
}

func (w ArtifactIntelligenceCopyWorker) runtime(formats []repository.Format) lifecycle.Runtime {
	return lifecycle.Runtime{
		Store:                w.Store,
		Kind:                 repository.LifecycleJobIntelligence,
		Formats:              formats,
		Name:                 "artifact intelligence copy",
		Operation:            "intelligence-copy",
		Metrics:              w.Metrics,
		LeaseRefreshInterval: w.LeaseRefreshInterval,
		LeaseProgressMessage: "copying artifact intelligence",
	}
}

func (w ArtifactIntelligenceCopyWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	w.runtime(w.formats()).Start(ctx, interval, 100, w.execute)
}
