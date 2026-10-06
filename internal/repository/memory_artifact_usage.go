package repository

import (
	"container/heap"
	"context"
	"sort"
	"strings"
)

// Keep only the offset plus one page while counting under the same read lock.
// The maximum-address heap discards rows beyond that bounded window.
type artifactUsageHeap []ArtifactUsageStat

func (h artifactUsageHeap) Len() int           { return len(h) }
func (h artifactUsageHeap) Less(i, j int) bool { return artifactUsageAddressLess(h[j], h[i]) }
func (h artifactUsageHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *artifactUsageHeap) Push(v any)        { *h = append(*h, v.(ArtifactUsageStat)) }
func (h *artifactUsageHeap) Pop() any          { a := *h; v := a[len(a)-1]; *h = a[:len(a)-1]; return v }

func (s *MemoryStore) QueryArtifactUsage(ctx context.Context, repository string, query ArtifactUsageQuery) (ArtifactUsagePage, error) {
	var page ArtifactUsagePage
	if err := query.Validate(repository); err != nil {
		return page, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	window := artifactUsageHeap{}
	capacity := query.Offset + query.Limit
	for _, stat := range s.artifactUsage {
		if err := ctx.Err(); err != nil {
			return page, err
		}
		if stat.Repository != repository {
			continue
		}
		page.Totals.Resources++
		page.Totals.DownloadCount += stat.DownloadCount
		page.Totals.TotalBytes += stat.TotalBytes
		if !strings.Contains(stat.Resource, query.Query) {
			continue
		}
		page.TotalCount++
		if len(window) < capacity {
			heap.Push(&window, stat)
		} else if artifactUsageAddressLess(stat, window[0]) {
			window[0] = stat
			heap.Fix(&window, 0)
		}
	}
	sort.Slice(window, func(i, j int) bool { return artifactUsageAddressLess(window[i], window[j]) })
	start := min(query.Offset, len(window))
	page.Items = make([]ArtifactUsageStat, len(window)-start)
	copy(page.Items, window[start:])
	return page, nil
}

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
