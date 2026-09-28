package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type NativeCargoPromotion struct {
	Store interface {
		repository.NativeCargoStore
		repository.HostedRepositoryStore
		repository.LifecycleJobStore
		repository.RepositoryQuarantineReadPolicyStore
		repository.ArtifactQuarantineStore
	}
	Objects      OCIObjectStore
	Intelligence repository.ArtifactIntelligenceStore
	Metrics      repository.BackgroundOperationMetrics
}

type CargoPromotionPayload struct {
	Format             repository.Format `json:"format"`
	SourceRepositoryID string            `json:"sourceRepositoryId"`
	Name               string            `json:"name"`
	Version            string            `json:"version"`
	Digest             string            `json:"digest"`
}

func (m NativeCargoPromotion) Enqueue(ctx context.Context, targetID, key string, payload CargoPromotionPayload) (repository.LifecycleJob, bool, error) {
	payload.Format = repository.FormatCargo
	body, err := json.Marshal(payload)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: targetID,
		Kind: repository.LifecycleJobPromotion, IdempotencyKey: key, Payload: body})
}

func (m NativeCargoPromotion) RunJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobPromotion, repository.FormatCargo, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		if m.Metrics != nil {
			m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatCargo, "started")
			m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatCargo, 1)
		}
		jobErr := m.run(ctx, job)
		if m.Metrics != nil {
			outcome := "completed"
			if jobErr != nil {
				outcome = "failed"
			}
			m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatCargo, outcome)
			m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatCargo, -1)
		}
		if firstErr == nil {
			firstErr = jobErr
		}
	}
	return firstErr
}

func (m NativeCargoPromotion) run(ctx context.Context, job repository.LifecycleJob) error {
	var payload CargoPromotionPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != repository.FormatCargo ||
		payload.SourceRepositoryID == "" || payload.SourceRepositoryID == job.RepositoryID ||
		!validCargoVersionCoordinate(payload.Name+"@"+payload.Version) || !validRepositoryDigest(payload.Digest) {
		return m.fail(ctx, job, "invalid Cargo promotion payload")
	}
	if m.Objects == nil {
		return m.fail(ctx, job, "Cargo object store is unavailable")
	}
	coordinate := payload.Name + "@" + payload.Version
	source, err := m.Store.GetCargoPublication(ctx, payload.SourceRepositoryID, payload.Name, payload.Version)
	if err != nil || source.Digest != payload.Digest {
		return m.fail(ctx, job, "source Cargo publication is unavailable")
	}
	objectCtx, releaseObject, err := repository.LockObjectKeys(ctx, []string{source.ObjectKey}, m.Store,
		repository.FormatCargo, m.Store.LockCargoObject)
	if err != nil {
		return m.fail(ctx, job, "Cargo promotion object coordination failed")
	}
	defer releaseObject()
	admissionCtx, releaseAdmission, err := repository.LockArtifactDistributionCoordinates(objectCtx, m.Store,
		[]repository.ArtifactDistributionCoordinate{
			{RepositoryID: payload.SourceRepositoryID, Format: repository.FormatCargo, Coordinate: coordinate},
			{RepositoryID: job.RepositoryID, Format: repository.FormatCargo, Coordinate: coordinate},
			{RepositoryID: job.RepositoryID, Format: repository.FormatCargo, Coordinate: "__hosted_capacity__"},
		})
	if err != nil {
		return m.fail(ctx, job, "Cargo promotion admission coordination failed")
	}
	defer releaseAdmission()
	selected, err := m.Store.GetCargoPublication(admissionCtx, payload.SourceRepositoryID, payload.Name, payload.Version)
	if err != nil || selected.Digest != payload.Digest || selected.ObjectKey != source.ObjectKey ||
		selected.MetadataDigest != source.MetadataDigest || selected.Yanked != source.Yanked {
		return m.fail(ctx, job, "source Cargo publication changed")
	}
	allowed, err := repository.ArtifactDistributionAllowedForDigests(admissionCtx, m.Store,
		payload.SourceRepositoryID, repository.FormatCargo, coordinate, []string{selected.Digest})
	if err != nil || !allowed {
		return m.fail(ctx, job, repository.ArtifactQuarantinedReason)
	}
	stored, err := m.Objects.Stat(admissionCtx, selected.ObjectKey)
	if err != nil || stored.Digest != selected.Digest || stored.Size != selected.Size {
		return m.fail(ctx, job, "source Cargo archive is missing or changed")
	}
	if err := cargoTargetDependenciesReachable(admissionCtx, m.Store, job.RepositoryID, selected.IndexRow); err != nil {
		return m.fail(ctx, job, fmt.Sprintf("target Cargo dependencies are unavailable: %v", err))
	}
	operationCtx := ctx
	workCtx, heartbeat, err := startLifecycleJobHeartbeat(admissionCtx, m.Store, job.ID, job.LeaseToken, time.Minute)
	if err != nil {
		return err
	}
	defer func() { _ = heartbeat.stop() }()
	if err := m.Store.RenewLifecycleJobLease(workCtx, job.ID, job.LeaseToken); err != nil {
		return err
	}
	releaseLease, err := m.Store.LockLifecycleJobLease(workCtx, job.ID, job.LeaseToken)
	if err != nil {
		return err
	}
	target := cargoDistributionTarget(selected, job.RepositoryID)
	if err := publishCargoDistribution(workCtx, m.Store, target); err != nil {
		releaseLease()
		return m.fail(operationCtx, job, fmt.Sprintf("publish target Cargo version failed: %v", err))
	}
	if err := repository.CopyArtifactIntelligenceOrEnqueue(workCtx, m.Intelligence, m.Store, job.RepositoryID,
		payload.SourceRepositoryID, repository.FormatCargo, coordinate, payload.Digest); err != nil && !errors.Is(err, repository.ErrArtifactIntelligenceDeferred) {
		releaseLease()
		return m.fail(operationCtx, job, fmt.Sprintf("copy Cargo intelligence failed: %v", err))
	}
	heartbeatErr := heartbeat.stop()
	completeErr := m.Store.CompleteLifecycleJob(operationCtx, job.ID, job.LeaseToken)
	releaseLease()
	if completeErr == nil {
		return nil
	}
	if heartbeatErr != nil {
		return heartbeatErr
	}
	return completeErr
}

func (m NativeCargoPromotion) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message)
	return errors.New(message)
}

func (m NativeCargoPromotion) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = m.RunJobs(ctx, 100)
		wake := notificationWake(ctx, m.Store, "artifact_gateway_lifecycle_jobs")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.RunJobs(ctx, 100)
			case <-wake:
				_ = m.RunJobs(ctx, 100)
			}
		}
	}()
}

func cargoDistributionTarget(source repository.CargoPublication, repositoryID string) repository.CargoPublication {
	source.RepositoryID = repositoryID
	source.CreatedAt, source.UpdatedAt = time.Time{}, time.Time{}
	source.CollectingAt, source.CollectedAt = time.Time{}, time.Time{}
	return source
}

func publishCargoDistribution(ctx context.Context, store repository.NativeCargoStore, target repository.CargoPublication) error {
	if existing, err := store.GetCargoPublication(ctx, target.RepositoryID, target.Name, target.Version); err == nil {
		if cargoDistributionEquivalent(existing, target) {
			return nil
		}
		return repository.ErrCargoPublicationConflict
	} else if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if _, _, err := store.ReserveCargoIdentity(ctx, target.CargoIdentityClaim); err != nil {
		return err
	}
	_, _, err := store.CommitCargoPublication(ctx, target)
	return err
}

func cargoDistributionEquivalent(left, right repository.CargoPublication) bool {
	return left.RepositoryID == right.RepositoryID && left.Name == right.Name && left.Version == right.Version &&
		left.Digest == right.Digest && left.MetadataDigest == right.MetadataDigest &&
		left.ObjectKey == right.ObjectKey && left.Size == right.Size && left.Publisher == right.Publisher &&
		left.PublishedAt.Equal(right.PublishedAt) &&
		string(left.IndexRow) == string(right.IndexRow)
}

// CargoReplication uses the common checkpoint worker and publishes metadata
// only after the immutable archive has been verified at the destination.
type CargoReplication struct {
	Store interface {
		repository.NativeCargoStore
		repository.ReplicationStore
		repository.RepositoryQuarantineReadPolicyStore
		repository.ArtifactQuarantineStore
	}
	Source, Destination OCIObjectStore
	ChunkBytes          int64
	Metrics             repository.BackgroundOperationMetrics
}

func (r CargoReplication) RunJobs(ctx context.Context, limit int) error {
	return (replication.Worker{Store: r.Store, Source: r.Source, Destination: r.Destination,
		ChunkBytes: r.ChunkBytes, Format: repository.FormatCargo, Publish: r.publish,
		LockObject: r.Store.LockCargoObject, AdmissionSnapshot: r.admissionSnapshot, Metrics: r.Metrics}).Run(ctx, limit)
}

func (r CargoReplication) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = r.RunJobs(ctx, 100)
		wake := notificationWake(ctx, r.Store, "artifact_gateway_replication_plans")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = r.RunJobs(ctx, 100)
			case <-wake:
				_ = r.RunJobs(ctx, 100)
			}
		}
	}()
}

func (r CargoReplication) admissionSnapshot(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) ([]string, bool, error) {
	name, version, valid := splitVersionCoordinate(plan.Coordinate)
	if !valid {
		return nil, false, repository.ErrInvalidCargoIdentity
	}
	source, err := r.Store.GetCargoPublication(ctx, plan.SourceRepositoryID, name, version)
	if err != nil {
		return nil, false, err
	}
	matched := len(checkpoints) == 1 && source.Digest == plan.Digest &&
		checkpoints[0].SourceObjectKey == source.ObjectKey && checkpoints[0].ObjectKey == source.ObjectKey &&
		checkpoints[0].Digest == source.Digest && checkpoints[0].Size == source.Size
	return []string{source.Digest}, matched, nil
}

func (r CargoReplication) publish(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) error {
	if plan.Format != repository.FormatCargo || len(checkpoints) != 1 || checkpoints[0].State != "verified" {
		return repository.ErrInvalidCargoIdentity
	}
	name, version, valid := splitVersionCoordinate(plan.Coordinate)
	if !valid {
		return repository.ErrInvalidCargoIdentity
	}
	source, err := r.Store.GetCargoPublication(ctx, plan.SourceRepositoryID, name, version)
	if err != nil || source.Digest != plan.Digest || checkpoints[0].SourceObjectKey != source.ObjectKey ||
		checkpoints[0].ObjectKey != source.ObjectKey || checkpoints[0].Digest != source.Digest || checkpoints[0].Size != source.Size {
		return repository.ErrUpstreamChanged
	}
	stored, err := r.Destination.Stat(ctx, source.ObjectKey)
	if err != nil || stored.Digest != source.Digest || stored.Size != source.Size {
		return repository.ErrUpstreamChanged
	}
	if err := cargoTargetDependenciesReachable(ctx, r.Store, plan.TargetRepositoryID, source.IndexRow); err != nil {
		return err
	}
	return publishCargoDistribution(ctx, r.Store, cargoDistributionTarget(source, plan.TargetRepositoryID))
}

type cargoDependencyStore interface {
	repository.NativeCargoStore
	repository.RepositoryQuarantineReadPolicyStore
	repository.ArtifactQuarantineStore
}

// Same-registry dependencies are not copied implicitly. A target version is
// published only when every direct dependency has a non-yanked, readable
// version matching its Cargo requirement. Dependencies pinned to an external
// registry remain that registry's responsibility.
func cargoTargetDependenciesReachable(ctx context.Context, store cargoDependencyStore, targetID string, row []byte) error {
	var entry cargo.IndexEntry
	if err := json.Unmarshal(row, &entry); err != nil {
		return err
	}
	for _, dependency := range entry.Dependencies {
		if dependency.Registry != nil {
			continue
		}
		name := dependency.Name
		if dependency.Package != nil {
			name = *dependency.Package
		}
		constraint, err := semver.NewConstraint(dependency.Requirement)
		if err != nil {
			return fmt.Errorf("%s requirement %q: %w", name, dependency.Requirement, err)
		}
		versions, err := store.ListCargoPublications(ctx, targetID, name)
		if errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("%s %s: %w", name, dependency.Requirement, repository.ErrNotFound)
		}
		if err != nil {
			return err
		}
		reachable := false
		for _, version := range versions {
			if version.Yanked {
				continue
			}
			parsed, parseErr := semver.NewVersion(version.Version)
			if parseErr != nil || !constraint.Check(parsed) {
				continue
			}
			blocked, policyErr := repository.QuarantinedArtifactReadBlocked(ctx, store, store, targetID,
				repository.FormatCargo, version.Name+"@"+version.Version, version.Digest)
			if policyErr != nil {
				return policyErr
			}
			if !blocked {
				reachable = true
				break
			}
		}
		if !reachable {
			return fmt.Errorf("%s %s: %w", name, dependency.Requirement, repository.ErrNotFound)
		}
	}
	return nil
}
