package app

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// NativeMavenRetention evaluates durable policy outside publish and request
// paths. Tombstoning leaves byte reclamation to NativeMavenMaintenance.
type NativeMavenRetention struct {
	Store interface {
		repository.HostedRepositoryStore
		repository.RepositoryRetentionPolicyStore
		repository.NativeMavenStore
	}
	Now func() time.Time
}

func (m NativeMavenRetention) Collect(ctx context.Context) error {
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
				byModule[mavenRetentionModule(artifact.Coordinate)] = append(byModule[mavenRetentionModule(artifact.Coordinate)], artifact)
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

func (m NativeMavenRetention) Start(ctx context.Context, interval time.Duration) {
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

func mavenRetentionModule(coordinate string) string {
	parts := strings.Split(coordinate, ":")
	if len(parts) < 2 {
		return coordinate
	}
	return parts[0] + ":" + parts[1]
}
