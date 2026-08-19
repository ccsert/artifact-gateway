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

func goModuleCoordinate(modulePath, version string) string {
	return modulePath + "@" + version
}

func (s *MemoryStore) goModuleVersionTombstonedLocked(repositoryID, modulePath, version string) bool {
	_, ok := s.artifactTombstones[repositoryID+"\x00"+string(FormatGo)+"\x00"+goModuleCoordinate(modulePath, version)]
	return ok
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

func (s *MemoryStore) PublishGoModule(_ context.Context, incoming GoModulePublication) (GoModuleVersion, bool, error) {
	publication, err := normalizeGoModulePublication(incoming, time.Now().UTC())
	if err != nil {
		return GoModuleVersion{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	version := publication.Version
	versionKey := goVersionKey(version.RepositoryID, version.Module, version.Version)
	if s.goModuleVersionTombstonedLocked(version.RepositoryID, version.Module, version.Version) {
		return GoModuleVersion{}, false, ErrArtifactTombstoned
	}
	if existing, ok := s.goVersions[versionKey]; ok {
		assets := make([]GoModuleAsset, 0, 3)
		for _, kind := range []string{"info", "mod", "zip"} {
			if asset, found := s.goAssets[goAssetKey(version.RepositoryID, version.Module, version.Version, kind)]; found {
				assets = append(assets, asset)
			}
		}
		if goModulePublicationMatches(assets, publication.Assets) {
			return existing, true, nil
		}
		return GoModuleVersion{}, false, ErrNameExists
	}
	var used, incomingSize int64
	for _, asset := range s.goAssets {
		if asset.RepositoryID == version.RepositoryID {
			used += asset.Size
		}
	}
	for _, asset := range publication.Assets {
		incomingSize += asset.Size
	}
	if quota := s.capacityQuotas[version.RepositoryID]; quota > 0 && used+incomingSize > quota {
		return GoModuleVersion{}, false, ErrQuotaExceeded
	}
	s.goVersions[versionKey] = version
	for _, asset := range publication.Assets {
		s.goAssets[goAssetKey(asset.RepositoryID, asset.Module, asset.Version, asset.Kind)] = asset
	}
	return version, false, nil
}

func (s *MemoryStore) ListGoModuleVersions(_ context.Context, repositoryID, modulePath string) ([]GoModuleVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := make([]GoModuleVersion, 0)
	for _, version := range s.goVersions {
		if version.RepositoryID == repositoryID && version.Module == modulePath && !s.goModuleVersionTombstonedLocked(repositoryID, modulePath, version.Version) {
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
	if !ok || s.goModuleVersionTombstonedLocked(repositoryID, modulePath, version) {
		return GoModuleVersion{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) TombstoneGoModuleVersion(_ context.Context, repositoryID, modulePath, version string) (GoModuleVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.goVersions[goVersionKey(repositoryID, modulePath, version)]
	if !ok || s.goModuleVersionTombstonedLocked(repositoryID, modulePath, version) {
		return GoModuleVersion{}, ErrNotFound
	}
	digest := ""
	for _, kind := range []string{"zip", "mod", "info"} {
		if asset, found := s.goAssets[goAssetKey(repositoryID, modulePath, version, kind)]; found {
			digest = asset.Digest
			break
		}
	}
	now := time.Now().UTC()
	coordinate := goModuleCoordinate(modulePath, version)
	s.artifactTombstones[repositoryID+"\x00"+string(FormatGo)+"\x00"+coordinate] = ArtifactTombstone{
		RepositoryID: repositoryID, Format: FormatGo, Coordinate: coordinate, Digest: digest, TombstonedAt: now,
	}
	return item, nil
}

func (s *MemoryStore) RestoreGoModuleVersion(_ context.Context, repositoryID, modulePath, version string) (GoModuleVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.goVersions[goVersionKey(repositoryID, modulePath, version)]
	coordinate := goModuleCoordinate(modulePath, version)
	tombstoneKey := repositoryID + "\x00" + string(FormatGo) + "\x00" + coordinate
	if !ok {
		return GoModuleVersion{}, ErrDisabled
	}
	if _, ok = s.artifactTombstones[tombstoneKey]; !ok {
		return GoModuleVersion{}, ErrNotFound
	}
	delete(s.artifactTombstones, tombstoneKey)
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
	if !ok || s.goModuleVersionTombstonedLocked(repositoryID, modulePath, version) {
		return GoModuleAsset{}, ErrNotFound
	}
	return asset, nil
}

func (s *MemoryStore) GoModuleObjectHasReference(_ context.Context, objectKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, asset := range s.goAssets {
		if asset.ObjectKey == objectKey {
			return true, nil
		}
	}
	return false, nil
}
