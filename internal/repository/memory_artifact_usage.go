package repository

import (
	"context"
	"sort"
)

// RecordArtifactUsage folds one download increment into the in-memory
// aggregate. RecordAudit calls it under the store lock for download audits;
// tests and seeds may call it directly.
func (s *MemoryStore) RecordArtifactUsage(_ context.Context, stat ArtifactUsageStat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordArtifactUsageLocked(stat)
	return nil
}

func (s *MemoryStore) recordArtifactUsageLocked(stat ArtifactUsageStat) {
	if s.artifactUsage == nil {
		s.artifactUsage = make(map[string]ArtifactUsageStat)
	}
	key := artifactUsageAddress(stat.Repository, stat.Format, stat.Resource)
	current, ok := s.artifactUsage[key]
	if !ok {
		current = ArtifactUsageStat{
			Repository:        stat.Repository,
			Format:            stat.Format,
			Resource:          stat.Resource,
			FirstDownloadedAt: stat.LastDownloadedAt,
		}
	}
	current.DownloadCount++
	current.TotalBytes += stat.TotalBytes
	if stat.LastDownloadedAt.Before(current.FirstDownloadedAt) {
		current.FirstDownloadedAt = stat.LastDownloadedAt
	}
	if !stat.LastDownloadedAt.Before(current.LastDownloadedAt) {
		current.LastDownloadedAt = stat.LastDownloadedAt
		current.LastActor = stat.LastActor
	}
	s.artifactUsage[key] = current
}

func (s *MemoryStore) ListArtifactUsage(_ context.Context, repository string, limit int) ([]ArtifactUsageStat, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := make([]ArtifactUsageStat, 0, len(s.artifactUsage))
	for key := range s.artifactUsage {
		stat := s.artifactUsage[key]
		if repository != "" && stat.Repository != repository {
			continue
		}
		stats = append(stats, stat)
	}
	sort.SliceStable(stats, func(i, j int) bool {
		if stats[i].DownloadCount != stats[j].DownloadCount {
			return stats[i].DownloadCount > stats[j].DownloadCount
		}
		return stats[i].Resource < stats[j].Resource
	})
	if len(stats) > limit {
		stats = stats[:limit]
	}
	return stats, nil
}

func (s *MemoryStore) WalkArtifactUsage(ctx context.Context, repository string, visit func(ArtifactUsageStat) error) error {
	s.mu.RLock()
	stats := make([]ArtifactUsageStat, 0, len(s.artifactUsage))
	for _, stat := range s.artifactUsage {
		if stat.Repository == repository {
			stats = append(stats, stat)
		}
	}
	s.mu.RUnlock()
	for _, stat := range stats {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(stat); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryStore) ArtifactUsageTotals(_ context.Context, repository string) (ArtifactUsageTotals, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var totals ArtifactUsageTotals
	for _, stat := range s.artifactUsage {
		if repository != "" && stat.Repository != repository {
			continue
		}
		totals.DownloadCount += stat.DownloadCount
		totals.TotalBytes += stat.TotalBytes
		totals.Resources++
	}
	return totals, nil
}
