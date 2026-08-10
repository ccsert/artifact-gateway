package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
		repository.LifecycleJobStore
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
	if limit <= 0 {
		limit = 100
	}
	formats := append([]repository.Format(nil), w.WorkerFormats...)
	if len(formats) == 0 {
		formats = repository.SupportedFormats()
	}
	var firstErr error
	remaining := limit
	for _, format := range formats {
		if remaining == 0 {
			break
		}
		jobs, err := w.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobScan, format, remaining)
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

func (w ArtifactScanWorker) run(ctx context.Context, job repository.LifecycleJob) error {
	var payload repository.ArtifactScanPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Format == "" || payload.Coordinate == "" || payload.Digest == "" {
		return w.fail(ctx, job, "invalid artifact scan payload")
	}
	if w.Metrics != nil {
		w.Metrics.RecordBackgroundOperation("scan", payload.Format, "started")
		w.Metrics.AddBackgroundOperationInFlight("scan", payload.Format, 1)
		defer w.Metrics.AddBackgroundOperationInFlight("scan", payload.Format, -1)
	}
	scanCtx, cancelScan := context.WithCancel(ctx)
	stopHeartbeat := w.startLeaseHeartbeat(scanCtx, cancelScan, job)
	fail := func(message string) error {
		if leaseErr := stopHeartbeat(); leaseErr != nil {
			message = fmt.Sprintf("artifact scan lease renewal failed: %v", leaseErr)
		}
		return w.fail(ctx, job, message)
	}
	artifact, err := w.Resolver.ResolveArtifactScan(scanCtx, job.RepositoryID, payload)
	if err != nil {
		return fail(fmt.Sprintf("resolve artifact scan assets failed: %v", err))
	}
	report, err := w.Scanner.Scan(scanCtx, artifact)
	if err != nil {
		return fail(fmt.Sprintf("artifact scanner failed: %v", err))
	}
	if err := w.mergeReport(scanCtx, artifact, report); err != nil {
		return fail(fmt.Sprintf("merge artifact scan report failed: %v", err))
	}
	if leaseErr := stopHeartbeat(); leaseErr != nil {
		return w.fail(ctx, job, fmt.Sprintf("artifact scan lease renewal failed: %v", leaseErr))
	}
	if err := w.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
		return err
	}
	if w.Metrics != nil {
		w.Metrics.RecordBackgroundOperation("scan", payload.Format, "completed")
	}
	return nil
}

func (w ArtifactScanWorker) startLeaseHeartbeat(ctx context.Context, cancelScan context.CancelFunc, job repository.LifecycleJob) func() error {
	heartbeatCtx, cancelHeartbeat := context.WithCancel(ctx)
	result := make(chan error, 1)
	interval := w.LeaseRefreshInterval
	if interval <= 0 {
		interval = 3 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				result <- nil
				return
			case <-ticker.C:
				err := w.Store.UpdateLifecycleJobProgress(heartbeatCtx, job.ID, job.LeaseToken, job.ProgressCurrent, job.ProgressTotal, "scanning artifact")
				if err == nil {
					continue
				}
				if heartbeatCtx.Err() != nil {
					result <- nil
					return
				}
				cancelScan()
				result <- err
				return
			}
		}
	}()
	return func() error {
		cancelHeartbeat()
		cancelScan()
		return <-result
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
	merged.Vulnerability = cloneVulnerability(report.Vulnerability)
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
	latest.Vulnerability = cloneVulnerability(report.Vulnerability)
	latest.UpdatedBy = scannerActor(report)
	_, err = w.Store.ReplaceArtifactIntelligence(ctx, latest, latest.Version)
	return err
}

func cloneVulnerability(value *repository.ArtifactVulnerabilitySummary) *repository.ArtifactVulnerabilitySummary {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func scannerActor(report scanning.Report) string {
	if report.Vulnerability != nil && report.Vulnerability.Scanner != "" {
		return "scanner:" + report.Vulnerability.Scanner
	}
	return "scanner:artifact-scanner"
}

func (w ArtifactScanWorker) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	_ = w.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message)
	if w.Metrics != nil {
		var payload repository.ArtifactScanPayload
		_ = json.Unmarshal(job.Payload, &payload)
		w.Metrics.RecordBackgroundOperation("scan", payload.Format, "failed")
	}
	return errors.New(message)
}

func (w ArtifactScanWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 || w.Scanner == nil || w.Resolver == nil {
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
