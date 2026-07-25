package maven

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// NativeMaintenance collects only old, unreferenced native Maven object
// intents. The store rechecks references while deleting.
type NativeMaintenance struct {
	Store   repository.NativeMavenStore
	Objects objectstore.Store
	Now     func() time.Time
}

func (m NativeMaintenance) Collect(ctx context.Context) error {
	now := time.Now
	if m.Now != nil {
		now = m.Now
	}
	intents, err := m.Store.ClaimExpiredMavenObjectIntents(ctx, now().UTC().Add(-24*time.Hour), 100)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		referenced, err := m.Store.MavenObjectIntentHasReference(ctx, intent.ObjectKey)
		if err != nil {
			return err
		}
		if referenced {
			_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, intent.ObjectKey, intent.ClaimToken)
			continue
		}
		if err := m.Objects.Delete(ctx, intent.ObjectKey); err != nil {
			_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, intent.ObjectKey, intent.ClaimToken)
			return err
		}
		if err := m.Store.DeleteClaimedMavenObjectIntent(ctx, intent.ObjectKey, intent.ClaimToken); err != nil {
			_ = m.Store.ReleaseClaimedMavenObjectIntent(ctx, intent.ObjectKey, intent.ClaimToken)
			return err
		}
	}
	return nil
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
