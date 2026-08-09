package repository

import (
	"context"
	"sort"
	"time"
)

func goVersionKey(repositoryID, modulePath, version string) string {
	return repositoryID + "\x00" + modulePath + "\x00" + version
}

func goAssetKey(repositoryID, modulePath, version, kind string) string {
	return goVersionKey(repositoryID, modulePath, version) + "\x00" + kind
}

func (s *MemoryStore) LockGoObject(_ context.Context, objectKey string) (func(), error) {
	return s.lockMemoryObject(s.goObjectLocks, objectKey)
}

func (s *MemoryStore) SyncGoProxyVersions(_ context.Context, repositoryID, modulePath string, versions []GoModuleVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for _, incoming := range versions {
		incoming.RepositoryID = repositoryID
		incoming.Module = modulePath
		key := goVersionKey(repositoryID, modulePath, incoming.Version)
		if existing, ok := s.goVersions[key]; ok {
			if incoming.PublishedAt.IsZero() {
				incoming.PublishedAt = existing.PublishedAt
			}
			if incoming.Publisher == "" {
				incoming.Publisher = existing.Publisher
			}
			incoming.CreatedAt = existing.CreatedAt
			incoming.CachedAt = existing.CachedAt
		}
		if incoming.CreatedAt.IsZero() {
			incoming.CreatedAt = now
		}
		if incoming.CachedAt.IsZero() {
			incoming.CachedAt = now
		}
		s.goVersions[key] = incoming
	}
	return nil
}

func (s *MemoryStore) PutGoModuleVersion(_ context.Context, incoming GoModuleVersion) (GoModuleVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := goVersionKey(incoming.RepositoryID, incoming.Module, incoming.Version)
	now := time.Now().UTC()
	if existing, ok := s.goVersions[key]; ok {
		if !incoming.PublishedAt.IsZero() {
			existing.PublishedAt = incoming.PublishedAt
		}
		if incoming.Publisher != "" {
			existing.Publisher = incoming.Publisher
		}
		existing.CachedAt = now
		s.goVersions[key] = existing
		return existing, nil
	}
	if incoming.CreatedAt.IsZero() {
		incoming.CreatedAt = now
	}
	if incoming.CachedAt.IsZero() {
		incoming.CachedAt = now
	}
	s.goVersions[key] = incoming
	return incoming, nil
}

func (s *MemoryStore) ListGoModuleVersions(_ context.Context, repositoryID, modulePath string) ([]GoModuleVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := make([]GoModuleVersion, 0)
	for _, version := range s.goVersions {
		if version.RepositoryID == repositoryID && version.Module == modulePath {
			versions = append(versions, version)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
	if len(versions) == 0 {
		return nil, ErrNotFound
	}
	return versions, nil
}

func (s *MemoryStore) GetGoModuleVersion(_ context.Context, repositoryID, modulePath, version string) (GoModuleVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.goVersions[goVersionKey(repositoryID, modulePath, version)]
	if !ok {
		return GoModuleVersion{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) CacheGoModuleAsset(_ context.Context, incoming GoModuleAsset) (GoModuleAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.goVersions[goVersionKey(incoming.RepositoryID, incoming.Module, incoming.Version)]; !ok {
		return GoModuleAsset{}, ErrNotFound
	}
	key := goAssetKey(incoming.RepositoryID, incoming.Module, incoming.Version, incoming.Kind)
	if existing, ok := s.goAssets[key]; ok {
		if existing.Digest != incoming.Digest || existing.ObjectKey != incoming.ObjectKey || existing.Size != incoming.Size || existing.SourceURL != incoming.SourceURL {
			return GoModuleAsset{}, ErrUpstreamChanged
		}
		return existing, nil
	}
	var used int64
	for _, asset := range s.goAssets {
		if asset.RepositoryID == incoming.RepositoryID {
			used += asset.Size
		}
	}
	if quota := s.capacityQuotas[incoming.RepositoryID]; quota > 0 && used+incoming.Size > quota {
		return GoModuleAsset{}, ErrQuotaExceeded
	}
	now := time.Now().UTC()
	if incoming.CreatedAt.IsZero() {
		incoming.CreatedAt = now
	}
	if incoming.CachedAt.IsZero() {
		incoming.CachedAt = now
	}
	s.goAssets[key] = incoming
	return incoming, nil
}

func (s *MemoryStore) GetGoModuleAsset(_ context.Context, repositoryID, modulePath, version, kind string) (GoModuleAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.goAssets[goAssetKey(repositoryID, modulePath, version, kind)]
	if !ok {
		return GoModuleAsset{}, ErrNotFound
	}
	return asset, nil
}
