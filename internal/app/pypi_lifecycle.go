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
	Metrics repository.BackgroundOperationMetrics
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
	files, err := m.Store.ListPyPIProjectFiles(ctx, payload.SourceRepositoryID, payload.Project)
	if err != nil {
		return m.fail(ctx, job, "source PyPI project is unavailable")
	}
	selected := make([]repository.PyPIFile, 0)
	digestMatched := false
	for _, file := range files {
		if file.Version != payload.Version {
			continue
		}
		if file.ObjectKey == "" {
			return m.fail(ctx, job, "source PyPI version is not fully cached")
		}
		digestMatched = digestMatched || file.Digest == payload.Digest
		file.RepositoryID = job.RepositoryID
		file.SourceURL = ""
		selected = append(selected, file)
	}
	if len(selected) == 0 || !digestMatched {
		return m.fail(ctx, job, "source PyPI version is unavailable")
	}
	objectKeys := make([]string, 0, len(selected))
	for _, file := range selected {
		objectKeys = append(objectKeys, file.ObjectKey)
	}
	release, err := repository.LockObjectKeys(ctx, objectKeys, m.Store.LockPyPIObject)
	if err != nil {
		return m.fail(ctx, job, "target PyPI object coordination failed")
	}
	defer release()
	if _, err = m.Store.PublishPyPIVersion(ctx, selected); err != nil {
		return m.fail(ctx, job, "publish target PyPI version failed")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
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
	return (replication.Worker{Store: r.Store, Source: r.Source, Destination: r.Destination, ChunkBytes: r.ChunkBytes, Format: repository.FormatPyPI, Publish: r.publish, LockObject: r.Store.LockPyPIObject, Metrics: r.Metrics}).Run(ctx, limit)
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
	sourceFiles, err := r.sourceFiles(ctx, plan.SourceRepositoryID, checkpoints)
	if err != nil {
		return err
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

func (r PyPIReplication) sourceFiles(ctx context.Context, repositoryID string, checkpoints []repository.ReplicationCheckpoint) ([]repository.PyPIFile, error) {
	var available []repository.PyPIFile
	after := ""
	for {
		projects, err := r.Store.ListPyPIProjects(ctx, repositoryID, "", 200, after)
		if err != nil {
			return nil, err
		}
		for _, project := range projects {
			files, err := r.Store.ListPyPIProjectFiles(ctx, repositoryID, project.Project)
			if err != nil {
				return nil, err
			}
			available = append(available, files...)
		}
		if len(projects) < 200 {
			break
		}
		after = projects[len(projects)-1].Project
	}
	selected := make([]repository.PyPIFile, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		found := false
		for _, file := range available {
			if checkpoint.SourceObjectKey == file.ObjectKey && checkpoint.Digest == file.Digest && checkpoint.Size == file.Size && strings.HasSuffix(checkpoint.ObjectKey, "/"+file.Filename) {
				selected = append(selected, file)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("source PyPI version is unavailable or changed")
		}
	}
	for _, file := range selected[1:] {
		if file.Project != selected[0].Project || file.Version != selected[0].Version {
			return nil, errors.New("PyPI replication checkpoints span multiple versions")
		}
	}
	return selected, nil
}

func pypiReplicationTargetObjectKey(repositoryID, filename, digest string) string {
	return "native/pypi/replication/" + repositoryID + "/" + strings.TrimPrefix(digest, "sha256:") + "/" + filename
}
