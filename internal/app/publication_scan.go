package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type publicationScanStore interface {
	repository.RepositorySecurityPolicyStore
	repository.LifecycleJobStore
	repository.HostedRepositoryStore
	RecordAudit(context.Context, repository.AuditRecord) error
}

type publicationScanScheduler struct {
	store            publicationScanStore
	scannerAvailable bool
	formats          []repository.Format
	metrics          *Metrics
}

const publicationScanAuditTimeout = 2 * time.Second

func newPublicationScanScheduler(store publicationScanStore, scannerAvailable bool, formats []repository.Format, metrics *Metrics) publicationScanScheduler {
	return publicationScanScheduler{
		store:            store,
		scannerAvailable: scannerAvailable,
		formats:          append([]repository.Format(nil), formats...),
		metrics:          metrics,
	}
}

func (s publicationScanScheduler) Schedule(ctx context.Context, repo repository.HostedRepository, coordinate, digest, actor string) error {
	if s.store == nil || !s.scannerAvailable || !publicationScanSupported(repo) || !scanFormatEnabled(s.formats, repo.Format) {
		return nil
	}
	policy, err := s.store.GetRepositorySecurityPolicy(ctx, repo.ID)
	if err != nil {
		return s.recordFailure(ctx, repo, coordinate, digest, actor, err)
	}
	if !policy.AutoScanOnPublish {
		return nil
	}

	key := publicationScanIdempotencyKey(repo.ID, repo.Format, coordinate, digest)
	_, _, err = repository.EnqueueArtifactScanJob(ctx, s.store, repo.ID, key, repository.ArtifactScanPayload{
		Format: repo.Format, Coordinate: coordinate, Digest: digest,
	})
	if err != nil {
		return s.recordFailure(ctx, repo, coordinate, digest, actor, err)
	}
	_ = s.store.RecordAudit(ctx, publicationScanAudit(repo, coordinate, digest, actor, repository.AuditResolved, http.StatusAccepted, ""))
	return nil
}

func publicationScanIdempotencyKey(repositoryID string, format repository.Format, coordinate, digest string) string {
	identity := strings.Join([]string{repositoryID, string(format), coordinate, digest}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return "publish-scan:" + hex.EncodeToString(sum[:])
}

func (s publicationScanScheduler) ScheduleRepository(ctx context.Context, repositoryID string, format repository.Format, coordinate, digest, actor string) error {
	if s.store == nil || !s.scannerAvailable {
		return nil
	}
	repo, err := s.store.GetHostedRepository(ctx, repositoryID)
	if err != nil {
		return s.recordFailure(ctx, repository.HostedRepository{ID: repositoryID, Name: repositoryID, Format: format}, coordinate, digest, actor, err)
	}
	return s.Schedule(ctx, repo, coordinate, digest, actor)
}

func publicationScanSupported(repo repository.HostedRepository) bool {
	if repo.Type == repository.RepositoryTypeProxy {
		return false
	}
	switch repo.Format {
	case repository.FormatMaven, repository.FormatOCI, repository.FormatRaw, repository.FormatConan, repository.FormatNPM, repository.FormatPyPI:
		return true
	default:
		return false
	}
}

func (s publicationScanScheduler) recordFailure(ctx context.Context, repo repository.HostedRepository, coordinate, digest, actor string, cause error) error {
	if s.metrics != nil {
		s.metrics.RecordBackgroundOperation("scan", repo.Format, "failed")
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publicationScanAuditTimeout)
	defer cancel()
	_ = s.store.RecordAudit(auditCtx, publicationScanAudit(repo, coordinate, digest, actor, repository.AuditStorageError, http.StatusInternalServerError, cause.Error()))
	return cause
}

func publicationScanAudit(repo repository.HostedRepository, coordinate, digest, actor string, outcome repository.AuditOutcome, status int, reason string) repository.AuditRecord {
	return repository.AuditRecord{
		Repository: repo.Name, GroupName: repo.Name, Actor: strings.TrimSpace(actor),
		Outcome: outcome, OccurredAt: time.Now().UTC(), Format: string(repo.Format),
		Resource: coordinate, Representation: digest, Operation: "artifact.scan.auto_enqueue",
		Status: status, CacheDisposition: "bypass", AuthorizationReason: reason,
	}
}
