package repository

import (
	"context"
	"sort"
	"strings"
	"time"
)

func aptAssetKey(repositoryID, path string) string { return repositoryID + "\x00" + path }

func (s *MemoryStore) LockAPTObject(_ context.Context, objectKey string) (func(), error) {
	return s.lockMemoryObject(s.aptObjectLocks, objectKey)
}

func (s *MemoryStore) GetAPTAsset(_ context.Context, repositoryID, path string) (APTAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.aptAssets[aptAssetKey(repositoryID, path)]
	if !ok {
		return APTAsset{}, ErrNotFound
	}
	return asset, nil
}

func (s *MemoryStore) CacheAPTAsset(_ context.Context, incoming APTAsset) (APTAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := aptAssetKey(incoming.RepositoryID, incoming.Path)
	if existing, ok := s.aptAssets[key]; ok {
		same := existing.Digest == incoming.Digest && existing.ObjectKey == incoming.ObjectKey && existing.Size == incoming.Size && existing.SourceURL == incoming.SourceURL
		if !same && !APTAssetMutable(incoming.Path) {
			return APTAsset{}, ErrUpstreamChanged
		}
		var used int64
		for _, asset := range s.aptAssets {
			if asset.RepositoryID == incoming.RepositoryID {
				used += asset.Size
			}
		}
		if quota := s.capacityQuotas[incoming.RepositoryID]; quota > 0 && used-existing.Size+incoming.Size > quota {
			return APTAsset{}, ErrQuotaExceeded
		}
		incoming.CreatedAt = existing.CreatedAt
		incoming.CachedAt = time.Now().UTC()
		s.aptAssets[key] = incoming
		return incoming, nil
	}
	var used int64
	for _, asset := range s.aptAssets {
		if asset.RepositoryID == incoming.RepositoryID {
			used += asset.Size
		}
	}
	if quota := s.capacityQuotas[incoming.RepositoryID]; quota > 0 && used+incoming.Size > quota {
		return APTAsset{}, ErrQuotaExceeded
	}
	now := time.Now().UTC()
	if incoming.CreatedAt.IsZero() {
		incoming.CreatedAt = now
	}
	if incoming.CachedAt.IsZero() {
		incoming.CachedAt = now
	}
	s.aptAssets[key] = incoming
	return incoming, nil
}

func (s *MemoryStore) ListAPTAssets(_ context.Context, repositoryID, prefix string, limit int, after string) ([]APTAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	items := make([]APTAsset, 0)
	for _, asset := range s.aptAssets {
		if asset.RepositoryID != repositoryID || !strings.HasPrefix(asset.Path, prefix) || (after != "" && asset.Path <= after) {
			continue
		}
		items = append(items, asset)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
