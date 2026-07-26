package maven

import (
	"context"
	"encoding/json"
	"fmt"
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
	jobs, err := m.Store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatMaven, limit)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := m.runReclaimJob(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

func (m NativeMaintenance) runReclaimJob(ctx context.Context, job repository.LifecycleJob) error {
	var payload reclaimPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ObjectKey == "" || payload.ClaimToken == "" {
		return m.failReclaimJob(ctx, job.ID, "invalid Maven reclaim payload")
	}
	active, err := m.Store.MavenObjectIntentClaimIsActive(ctx, payload.ObjectKey, payload.ClaimToken)
	if err != nil {
		return m.failReclaimJob(ctx, job.ID, "Maven object claim lookup failed")
	}
	if !active {
		return m.Store.CompleteLifecycleJob(ctx, job.ID)
	}
	referenced, err := m.Store.MavenObjectIntentHasReference(ctx, payload.ObjectKey)
	if err != nil {
		return m.releaseAndFail(ctx, job.ID, payload, "Maven object reference lookup failed")
	}
	if referenced {
		_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, payload.ObjectKey, payload.ClaimToken)
		return m.Store.CompleteLifecycleJob(ctx, job.ID)
	}
	if err := m.Objects.Delete(ctx, payload.ObjectKey); err != nil {
		return m.releaseAndFail(ctx, job.ID, payload, fmt.Sprintf("delete Maven object: %v", err))
	}
	if err := m.Store.DeleteClaimedMavenObjectIntent(ctx, payload.ObjectKey, payload.ClaimToken); err != nil {
		return m.releaseAndFail(ctx, job.ID, payload, "mark Maven object intent collected failed")
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID)
}

func (m NativeMaintenance) releaseAndFail(ctx context.Context, id string, payload reclaimPayload, message string) error {
	_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, payload.ObjectKey, payload.ClaimToken)
	if err := m.Store.FailLifecycleJob(ctx, id, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func (m NativeMaintenance) failReclaimJob(ctx context.Context, id, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, id, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

func (m NativeMaintenance) Start(ctx context.Context, interval time.Duration) {
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

// NativeRetention evaluates durable Maven retention policy outside publish and
// request paths. Tombstoning leaves byte reclamation to NativeMaintenance.
type NativeRetention struct {
	Store interface {
		repository.HostedRepositoryStore
		repository.RepositoryRetentionPolicyStore
		repository.NativeMavenStore
		repository.LifecycleJobStore
	}
	Now func() time.Time
}

// NativePromotion executes immutable Maven metadata promotions through durable
// lifecycle jobs. Object bytes remain content-addressed and are not copied.
type NativePromotion struct {
	Store interface {
		repository.NativeMavenStore
		repository.LifecycleJobStore
	}
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
	for _, job := range jobs {
		var p PromotionPayload
		if err := json.Unmarshal(job.Payload, &p); err != nil || p.SourceRepositoryID == "" || p.Coordinate == "" || p.Digest == "" || p.PromotionID == "" {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, "invalid Maven promotion payload")
			continue
		}
		if _, err := m.Store.PromoteMavenArtifact(ctx, repository.MavenPromotion{ID: p.PromotionID, SourceRepositoryID: p.SourceRepositoryID, TargetRepositoryID: job.RepositoryID, Coordinate: p.Coordinate, Digest: p.Digest}); err != nil {
			_ = m.Store.FailLifecycleJob(ctx, job.ID, "promote Maven artifact failed")
			continue
		}
		if err := m.Store.CompleteLifecycleJob(ctx, job.ID); err != nil {
			return err
		}
	}
	return nil
}

type retentionPayload struct {
	Format        repository.Format `json:"format"`
	PolicyVersion string            `json:"policyVersion"`
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
	for _, job := range jobs {
		if err := m.runJob(ctx, job); err != nil {
			return err
		}
	}
	return nil
}

func (m NativeRetention) runJob(ctx context.Context, job repository.LifecycleJob) error {
	var payload retentionPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.Format != repository.FormatMaven || payload.PolicyVersion == "" {
		return m.failRetentionJob(ctx, job.ID, "invalid Maven retention payload")
	}
	policy, err := m.Store.GetRepositoryRetentionPolicy(ctx, job.RepositoryID)
	if err != nil {
		return m.failRetentionJob(ctx, job.ID, "get Maven retention policy failed")
	}
	if policy.Version != payload.PolicyVersion {
		return m.failRetentionJob(ctx, job.ID, "Maven retention policy changed before execution")
	}
	candidates, err := m.PlanRepository(ctx, job.RepositoryID)
	if err != nil {
		return m.failRetentionJob(ctx, job.ID, "plan Maven retention failed")
	}
	for _, artifact := range candidates {
		if _, err = m.Store.TombstoneMavenArtifact(ctx, job.RepositoryID, artifact.ID); err != nil {
			return m.failRetentionJob(ctx, job.ID, "tombstone Maven retention candidate failed")
		}
	}
	return m.Store.CompleteLifecycleJob(ctx, job.ID)
}

func (m NativeRetention) failRetentionJob(ctx context.Context, id, message string) error {
	if err := m.Store.FailLifecycleJob(ctx, id, message); err != nil {
		return err
	}
	return fmt.Errorf("%s", message)
}

// PlanRepository returns the artifacts a retention run would tombstone without
// changing state. It is shared by execution and management dry-run callers.
func (m NativeRetention) PlanRepository(ctx context.Context, repositoryID string) ([]repository.MavenArtifact, error) {
	policy, err := m.Store.GetRepositoryRetentionPolicy(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	artifacts, err := m.Store.ListMavenArtifacts(ctx, repositoryID)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	cutoff := now().UTC().AddDate(0, 0, -policy.KeepDays)
	byModule := map[string][]repository.MavenArtifact{}
	for _, artifact := range artifacts {
		key := retentionModule(artifact.Coordinate)
		byModule[key] = append(byModule[key], artifact)
	}
	candidates := []repository.MavenArtifact{}
	for _, versions := range byModule {
		sort.SliceStable(versions, func(i, j int) bool { return versions[i].CreatedAt.After(versions[j].CreatedAt) })
		for index, artifact := range versions {
			if index >= policy.MinimumVersions && artifact.CreatedAt.Before(cutoff) {
				candidates = append(candidates, artifact)
			}
		}
	}
	return candidates, nil
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
