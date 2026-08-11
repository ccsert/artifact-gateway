package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/lifecycle"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
)

// ArtifactScanResolver resolves a queued identity into verified object-store
// assets. Implementations own format-specific metadata parsing.
type ArtifactScanResolver interface {
	ResolveArtifactScan(context.Context, string, repository.ArtifactScanPayload) (scanning.Artifact, error)
}

// ArtifactScanWorker executes scanner jobs with the same lease semantics as
// promotion and retention workers. Scanner reports are merged into existing
// intelligence without replacing publisher trust evidence.
type ArtifactScanWorker struct {
	Store interface {
		repository.ArtifactIntelligenceStore
		lifecycle.Store
	}
	Scanner              scanning.Scanner
	Resolver             ArtifactScanResolver
	WorkerFormats        []repository.Format
	LeaseRefreshInterval time.Duration
	Metrics              repository.BackgroundOperationMetrics
}

func (w ArtifactScanWorker) RunJobs(ctx context.Context, limit int) error {
	if w.Scanner == nil || w.Resolver == nil {
		return errors.New("artifact scanner worker is not configured")
	}
	return w.runtime(w.formats()).RunJobs(ctx, limit, w.execute)
}

func (w ArtifactScanWorker) formats() []repository.Format {
	formats := append([]repository.Format(nil), w.WorkerFormats...)
	if len(formats) == 0 {
		formats = repository.SupportedFormats()
	}
	return formats
}

func (w ArtifactScanWorker) execute(ctx context.Context, job repository.LifecycleJob) error {
	var payload repository.ArtifactScanPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Format == "" || payload.Coordinate == "" || payload.Digest == "" {
		return errors.New("invalid artifact scan payload")
	}
	artifact, err := w.Resolver.ResolveArtifactScan(ctx, job.RepositoryID, payload)
	if err != nil {
		return fmt.Errorf("resolve artifact scan assets failed: %v", err)
	}
	report, err := w.Scanner.Scan(ctx, artifact)
	if err != nil {
		return fmt.Errorf("artifact scanner failed: %v", err)
	}
	if err := w.mergeReport(ctx, artifact, report); err != nil {
		return fmt.Errorf("merge artifact scan report failed: %v", err)
	}
	return nil
}

func (w ArtifactScanWorker) runtime(formats []repository.Format) lifecycle.Runtime {
	return lifecycle.Runtime{
		Store:                w.Store,
		Kind:                 repository.LifecycleJobScan,
		Formats:              formats,
		Name:                 "artifact scan",
		Operation:            "scan",
		Metrics:              w.Metrics,
		LeaseRefreshInterval: w.LeaseRefreshInterval,
		LeaseProgressMessage: "scanning artifact",
	}
}

func (w ArtifactScanWorker) mergeReport(ctx context.Context, artifact scanning.Artifact, report scanning.Report) error {
	current, err := w.Store.GetArtifactIntelligence(ctx, artifact.RepositoryID, artifact.Format, artifact.Coordinate, artifact.Digest)
	expected := ""
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if err == nil {
		expected = current.Version
	} else {
		current = repository.ArtifactIntelligence{RepositoryID: artifact.RepositoryID, Format: artifact.Format, Coordinate: artifact.Coordinate, Digest: artifact.Digest}
	}
	merged := current
	merged.RepositoryID, merged.Format, merged.Coordinate, merged.Digest = artifact.RepositoryID, artifact.Format, artifact.Coordinate, artifact.Digest
	merged.SBOMs = append([]repository.ArtifactSBOM(nil), report.SBOMs...)
	merged.Licenses = append([]repository.ArtifactLicense(nil), report.Licenses...)
	merged.Vulnerability = repository.CloneArtifactVulnerabilitySummary(report.Vulnerability)
	merged.UpdatedBy = scannerActor(report)
	if _, err = w.Store.ReplaceArtifactIntelligence(ctx, merged, expected); err == nil {
		return nil
	} else if !errors.Is(err, repository.ErrVersionConflict) {
		return err
	}
	// A concurrent publisher may have updated provenance while this scan was
	// running. Refetch once and merge scanner-owned fields onto that version.
	latest, getErr := w.Store.GetArtifactIntelligence(ctx, artifact.RepositoryID, artifact.Format, artifact.Coordinate, artifact.Digest)
	if getErr != nil {
		return getErr
	}
	latest.SBOMs = append([]repository.ArtifactSBOM(nil), report.SBOMs...)
	latest.Licenses = append([]repository.ArtifactLicense(nil), report.Licenses...)
	latest.Vulnerability = repository.CloneArtifactVulnerabilitySummary(report.Vulnerability)
	latest.UpdatedBy = scannerActor(report)
	_, err = w.Store.ReplaceArtifactIntelligence(ctx, latest, latest.Version)
	return err
}

func scannerActor(report scanning.Report) string {
	if report.Vulnerability != nil && report.Vulnerability.Scanner != "" {
		return "scanner:" + report.Vulnerability.Scanner
	}
	return "scanner:artifact-scanner"
}

func (w ArtifactScanWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 || w.Scanner == nil || w.Resolver == nil {
		return
	}
	w.runtime(w.formats()).Start(ctx, interval, 100, w.execute)
}
