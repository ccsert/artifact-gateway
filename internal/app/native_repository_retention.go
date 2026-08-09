package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"time"

	mavenprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/maven"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type repositoryRetentionStore interface {
	repository.HostedRepositoryStore
	repository.RepositoryRetentionPolicyStore
	repository.LifecycleJobStore
	repository.NativeMavenStore
	repository.NativeOCIStore
	repository.NativeRawStore
	repository.NativeConanStore
}

type NativeRepositoryRetention struct {
	Store   repositoryRetentionStore
	Now     func() time.Time
	Metrics repository.BackgroundOperationMetrics
	// WorkerFormats limits execution on a format-specialized worker. An empty
	// slice preserves the single-node behavior and handles every format.
	WorkerFormats []string
}

type RepositoryRetentionCandidate struct {
	Format        repository.Format
	Coordinate    string
	Digest        string
	CreatedAt     time.Time
	Reasons       []string
	AgeDays       int
	VersionType   string
	CursorID      string
	mavenID       string
	ociName       string
	conanRef      string
	conanRevision string
	rawPath       string
}

type repositoryRetentionPayload struct {
	Format        repository.Format `json:"format"`
	PolicyVersion string            `json:"policyVersion"`
}

func (m NativeRepositoryRetention) Collect(ctx context.Context) error {
	if err := m.Schedule(ctx); err != nil {
		return err
	}
	return m.RunJobs(ctx, 200)
}

// Schedule discovers enabled repository policies and records one durable
// retention job per repository. It does not tombstone artifacts.
func (m NativeRepositoryRetention) Schedule(ctx context.Context) error {
	now := m.now()
	after := ""
	for {
		repositories, next, err := m.Store.ListHostedRepositories(ctx, 200, after)
		if err != nil {
			return err
		}
		for _, repo := range repositories {
			if repo.State != repository.RepositoryActive || repo.Type != repository.RepositoryTypeHosted || !supportsRepositoryRetention(repo.Format) {
				continue
			}
			policy, policyErr := m.Store.GetRepositoryRetentionPolicy(ctx, repo.ID)
			if policyErr != nil {
				return policyErr
			}
			if !policy.Enabled {
				continue
			}
			if _, _, err = m.EnqueueRepository(ctx, repo.ID, "scheduled:"+now.Format("2006-01-02")+":"+policy.Version); err != nil {
				return err
			}
		}
		if next == "" {
			return nil
		}
		after = next
	}
}

func (m NativeRepositoryRetention) EnqueueRepository(ctx context.Context, repositoryID, idempotencyKey string) (repository.LifecycleJob, bool, error) {
	repo, err := m.Store.GetHostedRepository(ctx, repositoryID)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	if repo.Type != repository.RepositoryTypeHosted || !supportsRepositoryRetention(repo.Format) {
		return repository.LifecycleJob{}, false, repository.ErrDisabled
	}
	policy, err := m.Store.GetRepositoryRetentionPolicy(ctx, repositoryID)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	payload, err := json.Marshal(repositoryRetentionPayload{Format: repo.Format, PolicyVersion: policy.Version})
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repositoryID, Kind: repository.LifecycleJobRetention, IdempotencyKey: idempotencyKey, Payload: payload})
}

func (m NativeRepositoryRetention) RunJobs(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	var firstErr error
	remaining := limit
	for _, format := range repository.SupportedFormats() {
		if !m.handlesFormat(format) {
			continue
		}
		if remaining <= 0 {
			break
		}
		jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobRetention, format, remaining)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			m.begin(format)
			if err := m.runJob(ctx, job); err != nil {
				m.end(format, "failed")
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			m.end(format, "completed")
		}
		remaining -= len(jobs)
	}
	return firstErr
}

func (m NativeRepositoryRetention) handlesFormat(format repository.Format) bool {
	if len(m.WorkerFormats) == 0 {
		return true
	}
	for _, candidate := range m.WorkerFormats {
		if candidate == string(format) {
			return true
		}
	}
	return false
}

func (m NativeRepositoryRetention) runJob(ctx context.Context, job repository.LifecycleJob) error {
	var payload repositoryRetentionPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || !supportsRepositoryRetention(payload.Format) || payload.PolicyVersion == "" {
		return m.fail(ctx, job, "invalid repository retention payload")
	}
	repo, err := m.Store.GetHostedRepository(ctx, job.RepositoryID)
	if err != nil || repo.Type != repository.RepositoryTypeHosted || repo.Format != payload.Format {
		return m.fail(ctx, job, "repository retention payload does not match repository")
	}
	policy, err := m.Store.GetRepositoryRetentionPolicy(ctx, job.RepositoryID)
	if err != nil {
		return m.fail(ctx, job, "get repository retention policy failed")
	}
	if policy.Version != payload.PolicyVersion {
		if err = m.Store.UpdateLifecycleJobProgress(ctx, job.ID, job.LeaseToken, 0, 0, "superseded by retention policy "+policy.Version); err != nil {
			return m.fail(ctx, job, "update superseded retention job failed")
		}
		return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
	}
	candidates, err := m.PlanRepositoryDetailed(ctx, job.RepositoryID, payload.Format)
	if err != nil {
		return m.fail(ctx, job, "plan repository retention failed")
	}
	if err = m.Store.UpdateLifecycleJobProgress(ctx, job.ID, job.LeaseToken, 0, len(candidates), "retention plan ready"); err != nil {
		return m.fail(ctx, job, "update repository retention progress failed")
	}
	for index, candidate := range candidates {
		err = m.tombstone(ctx, job.RepositoryID, candidate)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return m.fail(ctx, job, "tombstone repository retention candidate failed")
		}
		message := candidate.Coordinate
		if len(message) > 255 {
			message = message[:255]
		}
		if err = m.Store.UpdateLifecycleJobProgress(ctx, job.ID, job.LeaseToken, index+1, len(candidates), message); err != nil {
			return m.fail(ctx, job, "update repository retention progress failed")
		}
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func (m NativeRepositoryRetention) tombstone(ctx context.Context, repositoryID string, candidate RepositoryRetentionCandidate) error {
	switch candidate.Format {
	case repository.FormatMaven:
		_, err := m.Store.TombstoneMavenArtifact(ctx, repositoryID, candidate.mavenID)
		return err
	case repository.FormatOCI:
		return m.Store.DeleteOCIManifest(ctx, repositoryID, candidate.ociName, candidate.Digest)
	case repository.FormatConan:
		_, err := m.Store.TombstoneConanRecipeRevision(ctx, repositoryID, candidate.conanRef, candidate.conanRevision)
		return err
	case repository.FormatRaw:
		return m.Store.DeleteRawAsset(ctx, repositoryID, candidate.rawPath)
	default:
		return repository.ErrDisabled
	}
}

func (m NativeRepositoryRetention) PlanRepositoryDetailed(ctx context.Context, repositoryID string, format repository.Format) ([]RepositoryRetentionCandidate, error) {
	repo, err := m.Store.GetHostedRepository(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	if repo.Type != repository.RepositoryTypeHosted || repo.Format != format || !supportsRepositoryRetention(format) {
		return nil, repository.ErrDisabled
	}
	policy, err := m.Store.GetRepositoryRetentionPolicy(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	if !policy.Enabled {
		return []RepositoryRetentionCandidate{}, nil
	}
	coordinatePatterns, err := compileRepositoryRetentionPatterns(policy.CoordinatePatterns)
	if err != nil {
		return nil, fmt.Errorf("compile retention coordinate patterns: %w", err)
	}
	protectedPatterns, err := compileRepositoryRetentionPatterns(policy.ProtectedPatterns)
	if err != nil {
		return nil, fmt.Errorf("compile retention protected patterns: %w", err)
	}
	var candidates []RepositoryRetentionCandidate
	switch format {
	case repository.FormatMaven:
		candidates, err = m.planMaven(ctx, repositoryID)
	case repository.FormatOCI:
		candidates, err = m.planOCI(ctx, repositoryID, policy, coordinatePatterns, protectedPatterns)
	case repository.FormatConan:
		candidates, err = m.planConan(ctx, repositoryID, policy, coordinatePatterns, protectedPatterns)
	case repository.FormatRaw:
		candidates, err = m.planRaw(ctx, repositoryID, policy, coordinatePatterns, protectedPatterns)
	default:
		return nil, repository.ErrDisabled
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Coordinate == candidates[j].Coordinate {
			return candidates[i].CursorID < candidates[j].CursorID
		}
		return candidates[i].Coordinate < candidates[j].Coordinate
	})
	return candidates, nil
}

func (m NativeRepositoryRetention) planMaven(ctx context.Context, repositoryID string) ([]RepositoryRetentionCandidate, error) {
	detailed, err := (mavenprotocol.NativeRetention{Store: m.Store, Now: m.Now}).PlanRepositoryDetailed(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	candidates := make([]RepositoryRetentionCandidate, 0, len(detailed))
	for _, candidate := range detailed {
		candidates = append(candidates, RepositoryRetentionCandidate{Format: repository.FormatMaven, Coordinate: candidate.Artifact.Coordinate, Digest: candidate.Artifact.Digest, CreatedAt: candidate.Artifact.CreatedAt, Reasons: candidate.Reasons, AgeDays: candidate.AgeDays, VersionType: candidate.VersionType, CursorID: candidate.Artifact.ID, mavenID: candidate.Artifact.ID})
	}
	return candidates, nil
}

func (m NativeRepositoryRetention) planOCI(ctx context.Context, repositoryID string, policy repository.RepositoryRetentionPolicy, coordinatePatterns, protectedPatterns []*regexp.Regexp) ([]RepositoryRetentionCandidate, error) {
	var candidates []RepositoryRetentionCandidate
	afterName := ""
	for {
		names, err := m.Store.ListOCIManifestNames(ctx, repositoryID, 200, afterName)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			manifests, err := m.listOCIManifests(ctx, repositoryID, name)
			if err != nil {
				return nil, err
			}
			sort.SliceStable(manifests, func(i, j int) bool {
				if manifests[i].CreatedAt.Equal(manifests[j].CreatedAt) {
					return manifests[i].Digest > manifests[j].Digest
				}
				return manifests[i].CreatedAt.After(manifests[j].CreatedAt)
			})
			for index, manifest := range manifests {
				coordinate := manifest.Name + "@" + manifest.Digest
				keys := []string{coordinate, manifest.Name}
				for _, tag := range manifest.Tags {
					keys = append(keys, manifest.Name+":"+tag)
				}
				candidate, ok := m.versionedCandidate(repository.FormatOCI, coordinate, manifest.Digest, manifest.CreatedAt, index, policy, keys, coordinatePatterns, protectedPatterns)
				if ok {
					candidate.CursorID, candidate.ociName = manifest.Digest, manifest.Name
					candidates = append(candidates, candidate)
				}
			}
		}
		if len(names) < 200 {
			return candidates, nil
		}
		afterName = names[len(names)-1]
	}
}

func (m NativeRepositoryRetention) listOCIManifests(ctx context.Context, repositoryID, name string) ([]repository.OCIManifest, error) {
	var manifests []repository.OCIManifest
	after := ""
	for {
		page, err := m.Store.ListOCIManifests(ctx, repositoryID, name, 200, after)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, page...)
		if len(page) < 200 {
			return manifests, nil
		}
		after = page[len(page)-1].Digest
	}
}

func (m NativeRepositoryRetention) planConan(ctx context.Context, repositoryID string, policy repository.RepositoryRetentionPolicy, coordinatePatterns, protectedPatterns []*regexp.Regexp) ([]RepositoryRetentionCandidate, error) {
	var candidates []RepositoryRetentionCandidate
	after := ""
	for {
		references, err := m.Store.SearchConanReferences(ctx, repositoryID, "", 200, after)
		if err != nil {
			return nil, err
		}
		for _, reference := range references {
			revisions, err := m.Store.ListConanRecipeRevisions(ctx, repositoryID, reference.Reference)
			if err != nil {
				return nil, err
			}
			sort.SliceStable(revisions, func(i, j int) bool {
				if revisions[i].CreatedAt.Equal(revisions[j].CreatedAt) {
					return revisions[i].Revision > revisions[j].Revision
				}
				return revisions[i].CreatedAt.After(revisions[j].CreatedAt)
			})
			for index, revision := range revisions {
				coordinate := revision.Reference + "#" + revision.Revision
				candidate, ok := m.versionedCandidate(repository.FormatConan, coordinate, revision.Digest, revision.CreatedAt, index, policy, []string{coordinate, revision.Reference}, coordinatePatterns, protectedPatterns)
				if ok {
					candidate.CursorID, candidate.conanRef, candidate.conanRevision = revision.Revision, revision.Reference, revision.Revision
					candidates = append(candidates, candidate)
				}
			}
		}
		if len(references) < 200 {
			return candidates, nil
		}
		after = references[len(references)-1].Reference
	}
}

func (m NativeRepositoryRetention) planRaw(ctx context.Context, repositoryID string, policy repository.RepositoryRetentionPolicy, coordinatePatterns, protectedPatterns []*regexp.Regexp) ([]RepositoryRetentionCandidate, error) {
	var candidates []RepositoryRetentionCandidate
	after := ""
	for {
		assets, err := m.Store.ListRawAssets(ctx, repositoryID, "", 200, after)
		if err != nil {
			return nil, err
		}
		for _, asset := range assets {
			if !matchesRepositoryRetentionPatterns(coordinatePatterns, []string{asset.Path}, true) || matchesRepositoryRetentionPatterns(protectedPatterns, []string{asset.Path}, false) {
				continue
			}
			createdAt := m.retentionTime(asset.UpdatedAt)
			if !createdAt.Before(m.now().AddDate(0, 0, -policy.KeepDays)) {
				continue
			}
			candidates = append(candidates, RepositoryRetentionCandidate{Format: repository.FormatRaw, Coordinate: asset.Path, Digest: asset.Digest, CreatedAt: createdAt, Reasons: []string{"age"}, AgeDays: m.ageDays(createdAt), VersionType: "asset", CursorID: asset.Path, rawPath: asset.Path})
		}
		if len(assets) < 200 {
			return candidates, nil
		}
		after = assets[len(assets)-1].Path
	}
}

func (m NativeRepositoryRetention) versionedCandidate(format repository.Format, coordinate, digest string, createdAt time.Time, index int, policy repository.RepositoryRetentionPolicy, matchKeys []string, coordinatePatterns, protectedPatterns []*regexp.Regexp) (RepositoryRetentionCandidate, bool) {
	if index < policy.MinimumVersions || !matchesRepositoryRetentionPatterns(coordinatePatterns, matchKeys, true) || matchesRepositoryRetentionPatterns(protectedPatterns, matchKeys, false) {
		return RepositoryRetentionCandidate{}, false
	}
	createdAt = m.retentionTime(createdAt)
	reasons := make([]string, 0, 2)
	if createdAt.Before(m.now().AddDate(0, 0, -policy.KeepDays)) {
		reasons = append(reasons, "age")
	}
	if policy.MaximumVersions > 0 && index >= policy.MaximumVersions {
		reasons = append(reasons, "maximum_versions")
	}
	if len(reasons) == 0 {
		return RepositoryRetentionCandidate{}, false
	}
	return RepositoryRetentionCandidate{Format: format, Coordinate: coordinate, Digest: digest, CreatedAt: createdAt, Reasons: reasons, AgeDays: m.ageDays(createdAt), VersionType: "version"}, true
}

func (m NativeRepositoryRetention) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m NativeRepositoryRetention) retentionTime(value time.Time) time.Time {
	if value.IsZero() {
		return m.now()
	}
	return value.UTC()
}

func (m NativeRepositoryRetention) ageDays(createdAt time.Time) int {
	age := int(m.now().Sub(createdAt.UTC()).Hours() / 24)
	if age < 0 {
		return 0
	}
	return age
}

func (m NativeRepositoryRetention) fail(ctx context.Context, job repository.LifecycleJob, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func (m NativeRepositoryRetention) begin(format repository.Format) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", format, "started")
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", format, 1)
	}
}

func (m NativeRepositoryRetention) end(format repository.Format, outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", format, outcome)
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", format, -1)
	}
}

func (m NativeRepositoryRetention) Start(ctx context.Context, interval time.Duration) {
	m.StartScheduler(ctx, interval)
	m.StartWorker(ctx, interval)
}

func (m NativeRepositoryRetention) StartScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		collectionTicker := time.NewTicker(interval)
		defer collectionTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-collectionTicker.C:
				_ = m.Schedule(ctx)
			}
		}
	}()
}

func (m NativeRepositoryRetention) StartWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		_ = m.RunJobs(ctx, 200)
		wake := notificationWake(ctx, m.Store, "artifact_gateway_lifecycle_jobs")
		jobInterval := time.Minute
		if interval < jobInterval {
			jobInterval = interval
		}
		jobTicker := time.NewTicker(jobInterval)
		defer jobTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-jobTicker.C:
				_ = m.RunJobs(ctx, 200)
			case <-wake:
				_ = m.RunJobs(ctx, 200)
			}
		}
	}()
}

func supportsRepositoryRetention(format repository.Format) bool {
	return repository.FormatSupportsOperation(format, repository.RepositoryTypeHosted, repository.RepositoryOperationRetain)
}

func compileRepositoryRetentionPatterns(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		expression, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, expression)
	}
	return compiled, nil
}

func matchesRepositoryRetentionPatterns(patterns []*regexp.Regexp, values []string, emptyMatches bool) bool {
	if len(patterns) == 0 {
		return emptyMatches
	}
	for _, pattern := range patterns {
		for _, value := range values {
			if pattern.MatchString(value) {
				return true
			}
		}
	}
	return false
}
