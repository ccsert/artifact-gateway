package repository

import (
	"context"
	"sort"
	"strings"
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
		if asset.RepositoryID == version.RepositoryID && asset.CollectedAt.IsZero() {
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

func (s *MemoryStore) ListGoModules(_ context.Context, repositoryID, prefix string, limit int, after string) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool)
	modules := make([]string, 0)
	for _, version := range s.goVersions {
		if version.RepositoryID != repositoryID || version.Module <= after || !strings.HasPrefix(version.Module, prefix) || s.goModuleVersionTombstonedLocked(repositoryID, version.Module, version.Version) || seen[version.Module] {
			continue
		}
		seen[version.Module] = true
		modules = append(modules, version.Module)
	}
	sort.Strings(modules)
	if len(modules) > limit {
		modules = modules[:limit]
	}
	return modules, nil
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

func (s *MemoryStore) TombstoneGoModuleVersion(ctx context.Context, repositoryID, modulePath, version string) (GoModuleVersion, error) {
	s.mu.RLock()
	_, exists := s.goVersions[goVersionKey(repositoryID, modulePath, version)]
	alreadyTombstoned := s.goModuleVersionTombstonedLocked(repositoryID, modulePath, version)
	objectKeys := make([]string, 0, 3)
	for _, kind := range []string{"info", "mod", "zip"} {
		if asset, found := s.goAssets[goAssetKey(repositoryID, modulePath, version, kind)]; found {
			objectKeys = append(objectKeys, asset.ObjectKey)
		}
	}
	s.mu.RUnlock()
	if !exists || alreadyTombstoned {
		return GoModuleVersion{}, ErrNotFound
	}
	_, releaseObjects, err := LockObjectKeys(ctx, objectKeys, s, FormatGo, s.LockGoObject)
	if err != nil {
		return GoModuleVersion{}, err
	}
	defer releaseObjects()

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

func (s *MemoryStore) RestoreGoModuleVersion(ctx context.Context, repositoryID, modulePath, version string) (GoModuleVersion, error) {
	s.mu.RLock()
	_, ok := s.goVersions[goVersionKey(repositoryID, modulePath, version)]
	coordinate := goModuleCoordinate(modulePath, version)
	tombstoneKey := repositoryID + "\x00" + string(FormatGo) + "\x00" + coordinate
	_, tombstoned := s.artifactTombstones[tombstoneKey]
	objectKeys := make([]string, 0, 3)
	for _, kind := range []string{"info", "mod", "zip"} {
		if asset, found := s.goAssets[goAssetKey(repositoryID, modulePath, version, kind)]; found {
			objectKeys = append(objectKeys, asset.ObjectKey)
		}
	}
	s.mu.RUnlock()
	if !ok {
		return GoModuleVersion{}, ErrDisabled
	}
	if !tombstoned {
		return GoModuleVersion{}, ErrNotFound
	}
	_, releaseObjects, err := LockObjectKeys(ctx, objectKeys, s, FormatGo, s.LockGoObject)
	if err != nil {
		return GoModuleVersion{}, err
	}
	defer releaseObjects()
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.goVersions[goVersionKey(repositoryID, modulePath, version)]
	if !ok {
		return GoModuleVersion{}, ErrDisabled
	}
	if _, ok = s.artifactTombstones[tombstoneKey]; !ok {
		return GoModuleVersion{}, ErrNotFound
	}
	for _, kind := range []string{"info", "mod", "zip"} {
		asset, found := s.goAssets[goAssetKey(repositoryID, modulePath, version, kind)]
		if !found || !asset.CollectingAt.IsZero() || !asset.CollectedAt.IsZero() {
			return GoModuleVersion{}, ErrDisabled
		}
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
		if asset.RepositoryID == incoming.RepositoryID && asset.CollectedAt.IsZero() {
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
	if !ok || !asset.CollectedAt.IsZero() || s.goModuleVersionTombstonedLocked(repositoryID, modulePath, version) {
		return GoModuleAsset{}, ErrNotFound
	}
	return asset, nil
}

func (s *MemoryStore) GoModuleObjectHasReference(_ context.Context, objectKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, asset := range s.goAssets {
		if asset.ObjectKey == objectKey && asset.CollectedAt.IsZero() {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) ListReclaimableGoModuleObjects(_ context.Context, before time.Time, limit int, after string) ([]GoModuleObject, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	byObjectKey := make(map[string]GoModuleObject)
	for _, asset := range s.goAssets {
		if asset.ObjectKey == "" || !asset.CollectedAt.IsZero() {
			continue
		}
		tombstone, ok := s.artifactTombstones[asset.RepositoryID+"\x00"+string(FormatGo)+"\x00"+goModuleCoordinate(asset.Module, asset.Version)]
		if !ok {
			continue
		}
		object, exists := byObjectKey[asset.ObjectKey]
		if !exists || tombstone.TombstonedAt.After(object.TombstonedAt) {
			byObjectKey[asset.ObjectKey] = GoModuleObject{RepositoryID: asset.RepositoryID, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size, TombstonedAt: tombstone.TombstonedAt}
		}
	}
	objects := make([]GoModuleObject, 0, len(byObjectKey))
	for _, object := range byObjectKey {
		idempotencyKey := "go-tombstone-object:" + object.ObjectKey + ":" + object.TombstonedAt.UTC().Format(time.RFC3339Nano)
		hasGenerationJob := false
		for _, job := range s.lifecycleJobs {
			if job.Kind == LifecycleJobReclaim && job.IdempotencyKey == idempotencyKey {
				hasGenerationJob = true
				break
			}
		}
		if object.ObjectKey > after && object.TombstonedAt.Before(before) && !hasGenerationJob {
			objects = append(objects, object)
		}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].ObjectKey < objects[j].ObjectKey })
	if len(objects) > limit {
		objects = objects[:limit]
	}
	return objects, nil
}

func (s *MemoryStore) GoModuleObjectHasVisibleReference(_ context.Context, objectKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, asset := range s.goAssets {
		if asset.ObjectKey == objectKey && asset.CollectedAt.IsZero() && !s.goModuleVersionTombstonedLocked(asset.RepositoryID, asset.Module, asset.Version) {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) GoModuleObjectMatchesTombstone(_ context.Context, objectKey string, expected time.Time) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var newest time.Time
	found := false
	for _, asset := range s.goAssets {
		if asset.ObjectKey != objectKey || !asset.CollectedAt.IsZero() {
			continue
		}
		tombstone, ok := s.artifactTombstones[asset.RepositoryID+"\x00"+string(FormatGo)+"\x00"+goModuleCoordinate(asset.Module, asset.Version)]
		if !ok {
			continue
		}
		found = true
		if tombstone.TombstonedAt.After(newest) {
			newest = tombstone.TombstonedAt
		}
	}
	return found && newest.Equal(expected), nil
}

func (s *MemoryStore) MarkGoModuleObjectCollecting(_ context.Context, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	updated := false
	for key, asset := range s.goAssets {
		if asset.ObjectKey == objectKey && asset.CollectedAt.IsZero() && s.goModuleVersionTombstonedLocked(asset.RepositoryID, asset.Module, asset.Version) {
			if asset.CollectingAt.IsZero() {
				asset.CollectingAt = now
				s.goAssets[key] = asset
			}
			updated = true
		}
	}
	if !updated {
		return ErrNotFound
	}
	return nil
}

func (s *MemoryStore) MarkGoModuleObjectCollected(_ context.Context, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	updated := false
	for key, asset := range s.goAssets {
		if asset.ObjectKey == objectKey && asset.CollectedAt.IsZero() && s.goModuleVersionTombstonedLocked(asset.RepositoryID, asset.Module, asset.Version) {
			asset.CollectedAt = now
			asset.CollectingAt = time.Time{}
			s.goAssets[key] = asset
			updated = true
		}
	}
	if !updated {
		return ErrNotFound
	}
	return nil
}
