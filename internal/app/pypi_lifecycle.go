package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/replication"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type NativePyPIPromotion struct {
	Store interface {
		repository.NativePyPIStore
		repository.LifecycleJobStore
	}
	Intelligence repository.ArtifactIntelligenceStore
	Metrics      repository.BackgroundOperationMetrics
}

type PyPIPromotionPayload struct {
	Format             repository.Format `json:"format"`
	SourceRepositoryID string            `json:"sourceRepositoryId"`
	Project            string            `json:"project"`
	Version            string            `json:"version"`
	Digest             string            `json:"digest"`
}

func (m NativePyPIPromotion) Enqueue(ctx context.Context, targetID, key string, payload PyPIPromotionPayload) (repository.LifecycleJob, bool, error) {
	payload.Format = repository.FormatPyPI
	body, err := json.Marshal(payload)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: targetID, Kind: repository.LifecycleJobPromotion, IdempotencyKey: key, Payload: body})
}

func (m NativePyPIPromotion) RunJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobPromotion, repository.FormatPyPI, limit)
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

func (m NativePyPIPromotion) run(ctx context.Context, job repository.LifecycleJob) error {
	var payload PyPIPromotionPayload
	if json.Unmarshal(job.Payload, &payload) != nil || payload.Format != repository.FormatPyPI || payload.SourceRepositoryID == "" || normalizePyPIProject(payload.Project) != payload.Project || !pypiVersionPattern.MatchString(payload.Version) || !validRepositoryDigest(payload.Digest) {
		return m.fail(ctx, job, "invalid PyPI promotion payload")
	}
	coordinate := payload.Project + "@" + payload.Version
	published := false
	for attempt := 0; attempt < 3; attempt++ {
		snapshot, loadErr := m.promotionFiles(ctx, job, payload)
		if loadErr != nil {
			return m.fail(ctx, job, loadErr.Error())
		}
		objectKeys := make([]string, 0, len(snapshot))
		for _, file := range snapshot {
			objectKeys = append(objectKeys, file.ObjectKey)
		}
		objectCtx, releaseObjects, lockErr := repository.LockObjectKeys(ctx, objectKeys, m.Store, repository.FormatPyPI, m.Store.LockPyPIObject)
		if lockErr != nil {
			return m.fail(ctx, job, "target PyPI object coordination failed")
		}
		admissionCtx, releaseAdmission, lockErr := repository.LockArtifactDistributionCoordinates(objectCtx, m.Store, []repository.ArtifactDistributionCoordinate{
			{RepositoryID: payload.SourceRepositoryID, Format: repository.FormatPyPI, Coordinate: coordinate},
			{RepositoryID: job.RepositoryID, Format: repository.FormatPyPI, Coordinate: coordinate},
		})
		if lockErr != nil {
			releaseObjects()
			return m.fail(ctx, job, "coordinate PyPI distribution admission failed")
		}
		selected, loadErr := m.promotionFiles(admissionCtx, job, payload)
		if loadErr != nil {
			releaseAdmission()
			releaseObjects()
			return m.fail(ctx, job, loadErr.Error())
		}
		if !samePyPIPromotionFiles(snapshot, selected) {
			releaseAdmission()
			releaseObjects()
			continue
		}
		digests := make([]string, 0, len(selected))
		for _, file := range selected {
			digests = append(digests, file.Digest)
		}
		allowed, admissionErr := repository.ArtifactDistributionAllowedForDigests(admissionCtx, m.Store, payload.SourceRepositoryID, repository.FormatPyPI, coordinate, digests)
		if admissionErr != nil {
			releaseAdmission()
			releaseObjects()
			return m.fail(ctx, job, "evaluate PyPI artifact quarantine failed")
		}
		if !allowed {
			releaseAdmission()
			releaseObjects()
			return m.fail(ctx, job, repository.ArtifactQuarantinedReason)
		}
		_, publishErr := m.Store.PublishPyPIVersion(admissionCtx, selected)
		releaseAdmission()
		releaseObjects()
		if publishErr != nil {
			return m.fail(ctx, job, "publish target PyPI version failed")
		}
		published = true
		break
	}
	if !published {
		return m.fail(ctx, job, "source PyPI version changed while coordinating publication")
	}
	intelligenceErr := repository.CopyArtifactIntelligenceOrEnqueue(ctx, m.Intelligence, m.Store, job.RepositoryID, payload.SourceRepositoryID, repository.FormatPyPI, coordinate, payload.Digest)
	if intelligenceErr != nil && !errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) {
		return m.fail(ctx, job, fmt.Sprintf("copy PyPI artifact intelligence failed: %v", intelligenceErr))
	}
	if errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) && m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("intelligence-copy", repository.FormatPyPI, "deferred")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func (m NativePyPIPromotion) promotionFiles(ctx context.Context, job repository.LifecycleJob, payload PyPIPromotionPayload) ([]repository.PyPIFile, error) {
	files, err := m.Store.ListPyPIProjectFiles(ctx, payload.SourceRepositoryID, payload.Project)
	if err != nil {
		return nil, errors.New("source PyPI project is unavailable")
	}
	selected := make([]repository.PyPIFile, 0)
	digestMatched := false
	for _, file := range files {
		if file.Version != payload.Version {
			continue
		}
		if file.ObjectKey == "" {
			return nil, errors.New("source PyPI version is not fully cached")
		}
		digestMatched = digestMatched || file.Digest == payload.Digest
		file.RepositoryID = job.RepositoryID
		file.SourceURL = ""
		selected = append(selected, file)
	}
	if len(selected) == 0 || !digestMatched {
		return nil, errors.New("source PyPI version is unavailable")
	}
	return selected, nil
}

func samePyPIPromotionFiles(left, right []repository.PyPIFile) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Filename != right[index].Filename || left[index].ObjectKey != right[index].ObjectKey || left[index].Digest != right[index].Digest || left[index].Size != right[index].Size {
			return false
		}
	}
	return true
}

func (m NativePyPIPromotion) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message)
	return fmt.Errorf("%s", message)
}

func (m NativePyPIPromotion) Start(ctx context.Context, interval time.Duration) {
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

type PyPIReplication struct {
	Store interface {
		repository.NativePyPIStore
		repository.ReplicationStore
	}
	Source      OCIObjectStore
	Destination OCIObjectStore
	ChunkBytes  int64
	Metrics     repository.BackgroundOperationMetrics
}

func (r PyPIReplication) RunJobs(ctx context.Context, limit int) error {
	return (replication.Worker{Store: r.Store, Source: r.Source, Destination: r.Destination, ChunkBytes: r.ChunkBytes, Format: repository.FormatPyPI, Publish: r.publish, LockObject: r.Store.LockPyPIObject, AdmissionSnapshot: r.admissionSnapshot, Metrics: r.Metrics}).Run(ctx, limit)
}

func (r PyPIReplication) admissionSnapshot(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) ([]string, bool, error) {
	files, err := r.currentVersionFiles(ctx, plan)
	if err != nil {
		return nil, false, err
	}
	digests := make([]string, 0, len(files))
	for _, file := range files {
		digests = append(digests, file.Digest)
	}
	return digests, pypiReplicationSnapshotMatches(files, checkpoints), nil
}

func (r PyPIReplication) currentVersionFiles(ctx context.Context, plan repository.ReplicationPlan) ([]repository.PyPIFile, error) {
	project, version, ok := parsePyPIVersionCoordinate(plan.Coordinate)
	if !ok {
		return nil, errors.New("invalid PyPI replication coordinate")
	}
	files, err := r.Store.ListPyPIProjectFiles(ctx, plan.SourceRepositoryID, project)
	if err != nil {
		return nil, err
	}
	selected := make([]repository.PyPIFile, 0, len(files))
	for _, file := range files {
		if file.Version == version {
			selected = append(selected, file)
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("source PyPI version is unavailable")
	}
	return selected, nil
}

func (r PyPIReplication) Start(ctx context.Context, interval time.Duration) {
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

func (r PyPIReplication) publish(ctx context.Context, plan repository.ReplicationPlan, checkpoints []repository.ReplicationCheckpoint) error {
	if plan.Format != repository.FormatPyPI || len(checkpoints) == 0 {
		return errors.New("unsupported PyPI replication plan")
	}
	sourceFiles, err := r.currentVersionFiles(ctx, plan)
	if err != nil {
		return err
	}
	if !pypiReplicationSnapshotMatches(sourceFiles, checkpoints) {
		return errors.New("source PyPI replication snapshot changed")
	}
	for index := range sourceFiles {
		var checkpoint repository.ReplicationCheckpoint
		found := false
		for _, candidate := range checkpoints {
			if candidate.SourceObjectKey == sourceFiles[index].ObjectKey && candidate.Digest == sourceFiles[index].Digest && candidate.Size == sourceFiles[index].Size && strings.HasSuffix(candidate.ObjectKey, "/"+sourceFiles[index].Filename) {
				checkpoint, found = candidate, true
				break
			}
		}
		if !found {
			return errors.New("source PyPI replication checkpoint is missing")
		}
		sourceFiles[index].RepositoryID = plan.TargetRepositoryID
		sourceFiles[index].ObjectKey = checkpoint.ObjectKey
		sourceFiles[index].Size = checkpoint.Size
		sourceFiles[index].SourceURL = ""
	}
	_, err = r.Store.PublishPyPIVersion(ctx, sourceFiles)
	return err
}

func pypiReplicationSnapshotMatches(files []repository.PyPIFile, checkpoints []repository.ReplicationCheckpoint) bool {
	if len(files) == 0 || len(files) != len(checkpoints) {
		return false
	}
	matched := make([]bool, len(checkpoints))
	for _, file := range files {
		found := false
		for index, checkpoint := range checkpoints {
			if !matched[index] && checkpoint.SourceObjectKey == file.ObjectKey && checkpoint.Digest == file.Digest && checkpoint.Size == file.Size && strings.HasSuffix(checkpoint.ObjectKey, "/"+file.Filename) {
				matched[index] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func pypiReplicationTargetObjectKey(repositoryID, filename, digest string) string {
	return "native/pypi/replication/" + repositoryID + "/" + strings.TrimPrefix(digest, "sha256:") + "/" + filename
}
