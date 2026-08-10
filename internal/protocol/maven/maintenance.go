package maven

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

// NativeMaintenance collects only old, unreferenced native Maven object
// intents. The store rechecks references while deleting.
type NativeMaintenance struct {
	Store   MavenReclaimStore
	Objects objectstore.Store
	Now     func() time.Time
	Metrics repository.BackgroundOperationMetrics
}

type MavenReclaimStore interface {
	repository.NativeMavenStore
	repository.LifecycleJobStore
}

type reclaimPayload struct {
	Format     repository.Format `json:"format"`
	ObjectKey  string            `json:"objectKey"`
	ClaimToken string            `json:"claimToken"`
}

func (m NativeMaintenance) Collect(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	if err := m.EnqueueReclaimJobs(ctx, now().UTC().Add(-24*time.Hour), 100); err != nil {
		return err
	}
	return m.RunReclaimJobs(ctx, 100)
}

func (m NativeMaintenance) EnqueueReclaimJobs(ctx context.Context, before time.Time, limit int) error {
	intents, err := m.Store.ClaimExpiredMavenObjectIntents(ctx, before, limit)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		payload, err := json.Marshal(reclaimPayload{Format: repository.FormatMaven, ObjectKey: intent.ObjectKey, ClaimToken: intent.ClaimToken})
		if err != nil {
			return err
		}
		if _, _, err = m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: intent.RepositoryID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "maven-object:" + intent.ObjectKey + ":" + intent.ClaimToken, Payload: payload}); err != nil {
			_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, intent.ObjectKey, intent.ClaimToken)
			return err
		}
	}
	return nil
}

func (m NativeMaintenance) RunReclaimJobs(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 100
	}
	var firstErr error
	for remaining := limit; remaining > 0; {
		jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatMaven, remaining)
		if err != nil {
			return err
		}
		if len(jobs) == 0 {
			return firstErr
		}
		for _, job := range jobs {
			m.beginLifecycle()
			if err := m.runReclaimJob(ctx, job); err != nil {
				m.endLifecycle("failed")
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			m.endLifecycle("completed")
		}
		remaining -= len(jobs)
	}
	return firstErr
}

func (m NativeMaintenance) beginLifecycle() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatMaven, "started")
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatMaven, 1)
	}
}

func (m NativeMaintenance) endLifecycle(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatMaven, outcome)
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatMaven, -1)
	}
}

func (m NativeMaintenance) runReclaimJob(ctx context.Context, job repository.LifecycleJob) error {
	var payload reclaimPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ObjectKey == "" || payload.ClaimToken == "" {
		return m.failReclaimJob(ctx, job, "invalid Maven reclaim payload")
	}
	active, err := m.Store.MavenObjectIntentClaimIsActive(ctx, payload.ObjectKey, payload.ClaimToken)
	if err != nil {
		return m.failReclaimJob(ctx, job, "Maven object claim lookup failed")
	}
	if !active {
		return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
	}
	referenced, err := m.Store.MavenObjectIntentHasReference(ctx, payload.ObjectKey)
	if err != nil {
		return m.releaseAndFail(ctx, job, payload, "Maven object reference lookup failed")
	}
	if referenced {
		_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, payload.ObjectKey, payload.ClaimToken)
		return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
	}
	if err := m.Objects.Delete(ctx, payload.ObjectKey); err != nil {
		return m.releaseAndFail(ctx, job, payload, fmt.Sprintf("delete Maven object: %v", err))
	}
	if err := m.Store.DeleteClaimedMavenObjectIntent(ctx, payload.ObjectKey, payload.ClaimToken); err != nil {
		return m.releaseAndFail(ctx, job, payload, "mark Maven object intent collected failed")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func (m NativeMaintenance) releaseAndFail(ctx context.Context, job repository.LifecycleJob, payload reclaimPayload, message string) error {
	_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, payload.ObjectKey, payload.ClaimToken)
	if err := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func (m NativeMaintenance) failReclaimJob(ctx context.Context, job repository.LifecycleJob, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func (m NativeMaintenance) Start(ctx context.Context, interval time.Duration) {
	m.StartScheduler(ctx, interval)
	m.StartWorker(ctx, interval)
}

// StartScheduler only discovers reclaimable objects and records durable jobs.
func (m NativeMaintenance) StartScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now
				if m.Now != nil {
					now = m.Now
				}
				_ = m.EnqueueReclaimJobs(ctx, now().UTC().Add(-24*time.Hour), 100)
			}
		}
	}()
}

// StartWorker only claims and executes durable reclaim jobs.
func (m NativeMaintenance) StartWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		wake := notificationWake(ctx, m.Store, "artifact_gateway_lifecycle_jobs")
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.RunReclaimJobs(ctx, 100)
			case <-wake:
				_ = m.RunReclaimJobs(ctx, 100)
			}
		}
	}()
}

type notificationSource interface {
	Listen(context.Context, string) <-chan struct{}
}

func notificationWake(ctx context.Context, store any, channel string) <-chan struct{} {
	if source, ok := store.(notificationSource); ok {
		return source.Listen(ctx, channel)
	}
	return nil
}

// NativeRetention evaluates durable Maven retention policy outside publish and
// request paths. Tombstoning leaves byte reclamation to NativeMaintenance.
type NativeRetention struct {
	Store interface {
		repository.HostedRepositoryStore
		repository.RepositoryRetentionPolicyStore
		repository.NativeMavenStore
		repository.LifecycleJobStore
	}
	Now     func() time.Time
	Metrics repository.BackgroundOperationMetrics
}

// NativePromotion executes immutable Maven metadata promotions through durable
// lifecycle jobs. Object bytes remain content-addressed and are not copied.
type NativePromotion struct {
	Store interface {
		repository.NativeMavenStore
		repository.LifecycleJobStore
	}
	Intelligence repository.ArtifactIntelligenceStore
	Metrics      repository.BackgroundOperationMetrics
}
type PromotionPayload struct {
	Format             repository.Format `json:"format"`
	SourceRepositoryID string            `json:"sourceRepositoryId"`
	Coordinate         string            `json:"coordinate"`
	Digest             string            `json:"digest"`
	PromotionID        string            `json:"promotionId"`
}

func (m NativePromotion) Enqueue(ctx context.Context, targetRepositoryID, idempotencyKey string, payload PromotionPayload) (repository.LifecycleJob, bool, error) {
	payload.Format = repository.FormatMaven
	encoded, err := json.Marshal(payload)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: targetRepositoryID, Kind: repository.LifecycleJobPromotion, IdempotencyKey: idempotencyKey, Payload: encoded})
}
func (m NativePromotion) RunJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobPromotion, repository.FormatMaven, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		m.beginPromotion()
		var p PromotionPayload
		if err := json.Unmarshal(job.Payload, &p); err != nil || p.SourceRepositoryID == "" || p.Coordinate == "" || p.Digest == "" || p.PromotionID == "" {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, "invalid Maven promotion payload")
			m.endPromotion("failed")
			continue
		}
		if _, err := m.Store.PromoteMavenArtifact(ctx, repository.MavenPromotion{ID: p.PromotionID, SourceRepositoryID: p.SourceRepositoryID, TargetRepositoryID: job.RepositoryID, Coordinate: p.Coordinate, Digest: p.Digest}); err != nil {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, fmt.Sprintf("promote Maven artifact failed: %v", err))
			m.endPromotion("failed")
			continue
		}
		intelligenceErr := repository.CopyArtifactIntelligenceOrEnqueue(ctx, m.Intelligence, m.Store, job.RepositoryID, p.SourceRepositoryID, repository.FormatMaven, p.Coordinate, p.Digest)
		if intelligenceErr != nil && !errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, fmt.Sprintf("copy Maven artifact intelligence failed: %v", intelligenceErr))
			m.endPromotion("failed")
			continue
		}
		if errors.Is(intelligenceErr, repository.ErrArtifactIntelligenceDeferred) && m.Metrics != nil {
			m.Metrics.RecordBackgroundOperation("intelligence-copy", repository.FormatMaven, "deferred")
		}
		if err := m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
			m.endPromotion("failed")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		m.endPromotion("completed")
	}
	return firstErr
}
func (m NativePromotion) beginPromotion() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("promotion", repository.FormatMaven, "started")
		m.Metrics.AddBackgroundOperationInFlight("promotion", repository.FormatMaven, 1)
	}
}
func (m NativePromotion) endPromotion(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("promotion", repository.FormatMaven, outcome)
		m.Metrics.AddBackgroundOperationInFlight("promotion", repository.FormatMaven, -1)
	}
}

// Start runs durable promotion work outside the management request path. Failed
// jobs are claimed again by RunJobs, preserving the original idempotency key.
func (m NativePromotion) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
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

type retentionPayload struct {
	Format        repository.Format `json:"format"`
	PolicyVersion string            `json:"policyVersion"`
}

type RetentionCandidate struct {
	Artifact    repository.MavenArtifact
	Reasons     []string
	AgeDays     int
	VersionType string
}

func (m NativeRetention) Collect(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	after := ""
	for {
		repositories, next, err := m.Store.ListHostedRepositories(ctx, 200, after)
		if err != nil {
			return err
		}
		for _, repo := range repositories {
			if repo.Format != repository.FormatMaven || repo.State != repository.RepositoryActive {
				continue
			}
			policy, policyErr := m.Store.GetRepositoryRetentionPolicy(ctx, repo.ID)
			if policyErr != nil {
				return policyErr
			}
			if !policy.Enabled {
				continue
			}
			if _, _, err = m.EnqueueRepository(ctx, repo.ID, "scheduled:"+now().UTC().Format("2006-01-02")); err != nil {
				return err
			}
		}
		if next == "" {
			return m.RunJobs(ctx, 200)
		}
		after = next
	}
}

// EnqueueRepository records an idempotent retention execution bound to the
// current policy version. A worker must reject it if the policy changes first.
func (m NativeRetention) EnqueueRepository(ctx context.Context, repositoryID, idempotencyKey string) (repository.LifecycleJob, bool, error) {
	policy, err := m.Store.GetRepositoryRetentionPolicy(ctx, repositoryID)
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	payload, err := json.Marshal(retentionPayload{Format: repository.FormatMaven, PolicyVersion: policy.Version})
	if err != nil {
		return repository.LifecycleJob{}, false, err
	}
	return m.Store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: repositoryID, Kind: repository.LifecycleJobRetention, IdempotencyKey: idempotencyKey, Payload: payload})
}

func (m NativeRetention) RunJobs(ctx context.Context, limit int) error {
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobRetention, repository.FormatMaven, limit)
	if err != nil {
		return err
	}
	var firstErr error
	for _, job := range jobs {
		m.beginLifecycle()
		if err := m.runJob(ctx, job); err != nil {
			m.endLifecycle("failed")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		m.endLifecycle("completed")
	}
	return firstErr
}

func (m NativeRetention) beginLifecycle() {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatMaven, "started")
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatMaven, 1)
	}
}

func (m NativeRetention) endLifecycle(outcome string) {
	if m.Metrics != nil {
		m.Metrics.RecordBackgroundOperation("lifecycle", repository.FormatMaven, outcome)
		m.Metrics.AddBackgroundOperationInFlight("lifecycle", repository.FormatMaven, -1)
	}
}

func (m NativeRetention) runJob(ctx context.Context, job repository.LifecycleJob) error {
	var payload retentionPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Format != repository.FormatMaven || payload.PolicyVersion == "" {
		return m.failRetentionJob(ctx, job, "invalid Maven retention payload")
	}
	policy, err := m.Store.GetRepositoryRetentionPolicy(ctx, job.RepositoryID)
	if err != nil {
		return m.failRetentionJob(ctx, job, "get Maven retention policy failed")
	}
	if policy.Version != payload.PolicyVersion {
		return m.failRetentionJob(ctx, job, "Maven retention policy changed before execution")
	}
	candidates, err := m.PlanRepository(ctx, job.RepositoryID)
	if err != nil {
		return m.failRetentionJob(ctx, job, "plan Maven retention failed")
	}
	for _, artifact := range candidates {
		if _, err = m.Store.TombstoneMavenArtifact(ctx, job.RepositoryID, artifact.ID); err != nil {
			return m.failRetentionJob(ctx, job, "tombstone Maven retention candidate failed")
		}
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken)
}

func (m NativeRetention) failRetentionJob(ctx context.Context, job repository.LifecycleJob, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, job.ID, job.LeaseToken, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

// PlanRepository returns the artifacts a retention run would tombstone without
// changing state. It is shared by execution and management dry-run callers.
func (m NativeRetention) PlanRepository(ctx context.Context, repositoryID string) ([]repository.MavenArtifact, error) {
	detailed, err := m.PlanRepositoryDetailed(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	candidates := make([]repository.MavenArtifact, 0, len(detailed))
	for _, candidate := range detailed {
		candidates = append(candidates, candidate.Artifact)
	}
	return candidates, nil
}

// PlanRepositoryDetailed explains why each artifact is eligible. Matching
// rules are evaluated before age and count rules; protected coordinates always
// win, including when a module exceeds its configured maximum.
func (m NativeRetention) PlanRepositoryDetailed(ctx context.Context, repositoryID string) ([]RetentionCandidate, error) {
	policy, err := m.Store.GetRepositoryRetentionPolicy(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	if !policy.Enabled {
		return []RetentionCandidate{}, nil
	}
	coordinatePatterns, err := compileRetentionPatterns(policy.CoordinatePatterns)
	if err != nil {
		return nil, fmt.Errorf("compile retention coordinate patterns: %w", err)
	}
	protectedPatterns, err := compileRetentionPatterns(policy.ProtectedPatterns)
	if err != nil {
		return nil, fmt.Errorf("compile retention protected patterns: %w", err)
	}
	artifacts, err := m.Store.ListMavenArtifacts(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	nowUTC := now().UTC()
	byModule := map[string][]repository.MavenArtifact{}
	for _, artifact := range artifacts {
		key := retentionModule(artifact.Coordinate)
		byModule[key] = append(byModule[key], artifact)
	}
	candidates := []RetentionCandidate{}
	for _, versions := range byModule {
		sort.SliceStable(versions, func(i, j int) bool {
			if versions[i].CreatedAt.Equal(versions[j].CreatedAt) {
				return versions[i].ID > versions[j].ID
			}
			return versions[i].CreatedAt.After(versions[j].CreatedAt)
		})
		for index, artifact := range versions {
			if index < policy.MinimumVersions || !matchesAnyRetentionPattern(coordinatePatterns, artifact.Coordinate, true) || matchesAnyRetentionPattern(protectedPatterns, artifact.Coordinate, false) {
				continue
			}
			versionType := "release"
			keepDays := policy.KeepDays
			if isMavenSnapshotCoordinate(artifact.Coordinate) {
				versionType = "snapshot"
				keepDays = policy.SnapshotKeepDays
			}
			reasons := []string{}
			if artifact.CreatedAt.Before(nowUTC.AddDate(0, 0, -keepDays)) {
				reasons = append(reasons, "age")
			}
			if policy.MaximumVersions > 0 && index >= policy.MaximumVersions {
				reasons = append(reasons, "maximum_versions")
			}
			if len(reasons) > 0 {
				ageDays := int(nowUTC.Sub(artifact.CreatedAt.UTC()).Hours() / 24)
				if ageDays < 0 {
					ageDays = 0
				}
				candidates = append(candidates, RetentionCandidate{Artifact: artifact, Reasons: reasons, AgeDays: ageDays, VersionType: versionType})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Artifact.Coordinate == candidates[j].Artifact.Coordinate {
			return candidates[i].Artifact.ID < candidates[j].Artifact.ID
		}
		return candidates[i].Artifact.Coordinate < candidates[j].Artifact.Coordinate
	})
	return candidates, nil
}

func compileRetentionPatterns(patterns []string) ([]*regexp.Regexp, error) {
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

func matchesAnyRetentionPattern(patterns []*regexp.Regexp, coordinate string, emptyMatches bool) bool {
	if len(patterns) == 0 {
		return emptyMatches
	}
	for _, pattern := range patterns {
		if pattern.MatchString(coordinate) {
			return true
		}
	}
	return false
}

func isMavenSnapshotCoordinate(coordinate string) bool {
	parts := strings.Split(coordinate, ":")
	return len(parts) >= 3 && strings.HasSuffix(strings.ToUpper(parts[2]), "-SNAPSHOT")
}

func (m NativeRetention) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = m.Collect(ctx)
			}
		}
	}()
}

func retentionModule(coordinate string) string {
	parts := strings.Split(coordinate, ":")
	if len(parts) < 2 {
		return coordinate
	}
	return parts[0] + ":" + parts[1]
}
