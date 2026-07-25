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
	}
	Now func() time.Time
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
			policy, err := m.Store.GetRepositoryRetentionPolicy(ctx, repo.ID)
			if err != nil {
				return err
			}
			artifacts, err := m.Store.ListMavenArtifacts(ctx, repo.ID)
			if err != nil {
				return err
			}
			byModule := map[string][]repository.MavenArtifact{}
			for _, artifact := range artifacts {
				byModule[retentionModule(artifact.Coordinate)] = append(byModule[retentionModule(artifact.Coordinate)], artifact)
			}
			cutoff := now().UTC().AddDate(0, 0, -policy.KeepDays)
			for _, versions := range byModule {
				sort.SliceStable(versions, func(i, j int) bool { return versions[i].CreatedAt.After(versions[j].CreatedAt) })
				for index, artifact := range versions {
					if index >= policy.MinimumVersions && artifact.CreatedAt.Before(cutoff) {
						if _, err = m.Store.TombstoneMavenArtifact(ctx, repo.ID, artifact.ID); err != nil {
							return err
						}
					}
				}
			}
		}
		if next == "" {
			return nil
		}
		after = next
	}
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
