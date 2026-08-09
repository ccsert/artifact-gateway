package repository

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

func npmPackageKey(repositoryID, name string) string {
	return repositoryID + "\x00" + name
}

func npmVersionKey(repositoryID, name, version string) string {
	return repositoryID + "\x00" + name + "\x00" + version
}

func (s *MemoryStore) PublishNPMVersion(_ context.Context, version NPMVersion, distTags map[string]string) (NPMVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	versionKey := npmVersionKey(version.RepositoryID, version.PackageName, version.Version)
	if _, exists := s.npmVersions[versionKey]; exists {
		return NPMVersion{}, ErrNameExists
	}
	for _, target := range distTags {
		if target == version.Version {
			continue
		}
		if _, exists := s.npmVersions[npmVersionKey(version.RepositoryID, version.PackageName, target)]; !exists {
			return NPMVersion{}, ErrNotFound
		}
	}

	now := time.Now().UTC()
	version.CreatedAt = now
	version.CachedAt = now
	version.State = "visible"
	version.Manifest = append([]byte(nil), version.Manifest...)
	s.npmVersions[versionKey] = version

	packageKey := npmPackageKey(version.RepositoryID, version.PackageName)
	pkg, exists := s.npmPackages[packageKey]
	if !exists {
		pkg = NPMPackage{
			RepositoryID: version.RepositoryID,
			Name:         version.PackageName,
			DistTags:     make(map[string]string),
			CreatedAt:    now,
		}
	}
	for tag, target := range distTags {
		pkg.DistTags[tag] = target
	}
	pkg.UpdatedAt = now
	s.npmPackages[packageKey] = pkg
	return cloneNPMVersion(version), nil
}

func (s *MemoryStore) LockNPMProxy(_ context.Context, key string) (func(), error) {
	s.mu.Lock()
	lock := s.npmProxyLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		s.npmProxyLocks[key] = lock
	}
	s.mu.Unlock()
	lock.Lock()
	return lock.Unlock, nil
}

func (s *MemoryStore) LockNPMObject(_ context.Context, objectKey string) (func(), error) {
	return s.lockMemoryObject(s.npmObjectLocks, objectKey)
}

func (s *MemoryStore) SyncNPMProxyPackage(_ context.Context, incoming NPMPackage) (NPMPackage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	packageKey := npmPackageKey(incoming.RepositoryID, incoming.Name)
	existing, exists := s.npmPackages[packageKey]
	if exists && existing.SourceEndpoint != "" && existing.SourceEndpoint != incoming.SourceEndpoint {
		for key, version := range s.npmVersions {
			if version.RepositoryID == incoming.RepositoryID && version.PackageName == incoming.Name {
				delete(s.npmVersions, key)
			}
		}
		existing = NPMPackage{}
		exists = false
	}
	available := make(map[string]bool, len(incoming.Versions))
	for _, version := range incoming.Versions {
		available[version.Version] = true
		stored, versionExists := s.npmVersions[npmVersionKey(incoming.RepositoryID, incoming.Name, version.Version)]
		if versionExists {
			available[stored.Version] = true
			if stored.ObjectKey != "" && (stored.Integrity != version.Integrity || stored.Shasum != version.Shasum || stored.UpstreamTarball != version.UpstreamTarball) {
				return NPMPackage{}, ErrUpstreamChanged
			}
		}
	}
	for _, target := range incoming.DistTags {
		if !available[target] {
			return NPMPackage{}, ErrNotFound
		}
	}
	for _, version := range incoming.Versions {
		key := npmVersionKey(incoming.RepositoryID, incoming.Name, version.Version)
		if stored, ok := s.npmVersions[key]; ok && stored.ObjectKey != "" {
			version.Digest = stored.Digest
			version.ObjectKey = stored.ObjectKey
			version.Size = stored.Size
			version.CachedAt = stored.CachedAt
		}
		version.RepositoryID = incoming.RepositoryID
		version.PackageName = incoming.Name
		version.Manifest = append([]byte(nil), version.Manifest...)
		if version.CreatedAt.IsZero() {
			version.CreatedAt = now
		}
		s.npmVersions[key] = version
	}
	if !exists {
		existing = NPMPackage{RepositoryID: incoming.RepositoryID, Name: incoming.Name, CreatedAt: now}
	}
	existing.DistTags = cloneStringMap(incoming.DistTags)
	existing.SourceEndpoint = incoming.SourceEndpoint
	existing.UpstreamETag = incoming.UpstreamETag
	existing.UpstreamModified = incoming.UpstreamModified
	existing.MetadataExpiresAt = incoming.MetadataExpiresAt
	existing.NegativeExpiresAt = time.Time{}
	existing.Negative = false
	existing.UpdatedAt = now
	s.npmPackages[packageKey] = existing
	return cloneNPMPackage(existing), nil
}

func (s *MemoryStore) StoreNPMProxyNegative(_ context.Context, incoming NPMPackage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	key := npmPackageKey(incoming.RepositoryID, incoming.Name)
	pkg, exists := s.npmPackages[key]
	if exists && pkg.SourceEndpoint != "" && pkg.SourceEndpoint != incoming.SourceEndpoint {
		for versionKey, version := range s.npmVersions {
			if version.RepositoryID == incoming.RepositoryID && version.PackageName == incoming.Name {
				delete(s.npmVersions, versionKey)
			}
		}
		pkg = NPMPackage{}
		exists = false
	}
	if !exists {
		pkg = NPMPackage{RepositoryID: incoming.RepositoryID, Name: incoming.Name, CreatedAt: now}
	}
	pkg.SourceEndpoint = incoming.SourceEndpoint
	pkg.MetadataExpiresAt = time.Time{}
	pkg.NegativeExpiresAt = incoming.NegativeExpiresAt
	pkg.Negative = true
	pkg.UpdatedAt = now
	s.npmPackages[key] = pkg
	return nil
}

func (s *MemoryStore) CacheNPMProxyTarball(_ context.Context, incoming NPMVersion) (NPMVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := npmVersionKey(incoming.RepositoryID, incoming.PackageName, incoming.Version)
	stored, ok := s.npmVersions[key]
	if !ok || stored.UpstreamTarball == "" {
		return NPMVersion{}, ErrNotFound
	}
	if stored.ObjectKey != "" {
		if stored.Digest != incoming.Digest {
			return NPMVersion{}, ErrUpstreamChanged
		}
		return cloneNPMVersion(stored), nil
	}
	var used int64
	for _, version := range s.npmVersions {
		if version.RepositoryID == incoming.RepositoryID {
			used += version.Size
		}
	}
	if quota := s.capacityQuotas[incoming.RepositoryID]; quota > 0 && used+incoming.Size > quota {
		return NPMVersion{}, ErrQuotaExceeded
	}
	stored.Digest = incoming.Digest
	stored.ObjectKey = incoming.ObjectKey
	stored.Size = incoming.Size
	stored.CachedAt = incoming.CachedAt
	s.npmVersions[key] = stored
	return cloneNPMVersion(stored), nil
}

func (s *MemoryStore) GetNPMPackage(_ context.Context, repositoryID, name string) (NPMPackage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pkg, ok := s.npmPackages[npmPackageKey(repositoryID, name)]
	if !ok {
		return NPMPackage{}, ErrNotFound
	}
	pkg = cloneNPMPackage(pkg)
	for _, version := range s.npmVersions {
		if version.RepositoryID == repositoryID && version.PackageName == name && version.State != "deleted" {
			pkg.Versions = append(pkg.Versions, cloneNPMVersion(version))
		}
	}
	if len(pkg.Versions) == 0 && !pkg.Negative {
		return NPMPackage{}, ErrNotFound
	}
	sort.Slice(pkg.Versions, func(i, j int) bool {
		if !pkg.Versions[i].CreatedAt.Equal(pkg.Versions[j].CreatedAt) {
			return pkg.Versions[i].CreatedAt.After(pkg.Versions[j].CreatedAt)
		}
		return pkg.Versions[i].Version > pkg.Versions[j].Version
	})
	return pkg, nil
}

func (s *MemoryStore) ListNPMVersions(_ context.Context, repositoryID, name string) ([]NPMVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := make([]NPMVersion, 0)
	for _, version := range s.npmVersions {
		if version.RepositoryID == repositoryID && version.PackageName == name && version.State != "deleted" {
			versions = append(versions, cloneNPMVersion(version))
		}
	}
	sort.Slice(versions, func(i, j int) bool {
		if versions[i].CreatedAt.Equal(versions[j].CreatedAt) {
			return versions[i].Version > versions[j].Version
		}
		return versions[i].CreatedAt.After(versions[j].CreatedAt)
	})
	return versions, nil
}

func (s *MemoryStore) GetNPMVersion(_ context.Context, repositoryID, name, version string) (NPMVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.npmVersions[npmVersionKey(repositoryID, name, version)]
	if !ok || item.State == "deleted" {
		return NPMVersion{}, ErrNotFound
	}
	return cloneNPMVersion(item), nil
}

func (s *MemoryStore) TombstoneNPMVersion(_ context.Context, repositoryID, name, version string) (NPMVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := npmVersionKey(repositoryID, name, version)
	item, ok := s.npmVersions[key]
	if !ok || item.State == "deleted" {
		return NPMVersion{}, ErrNotFound
	}
	now := time.Now().UTC()
	item.State, item.DeletedAt = "deleted", now
	s.npmVersions[key] = item
	packageKey := npmPackageKey(repositoryID, name)
	pkg := s.npmPackages[packageKey]
	for tag, target := range pkg.DistTags {
		if target == version {
			delete(pkg.DistTags, tag)
		}
	}
	pkg.UpdatedAt = now
	s.npmPackages[packageKey] = pkg
	coordinate := name + "@" + version
	s.artifactTombstones[repositoryID+"\x00"+string(FormatNPM)+"\x00"+coordinate] = ArtifactTombstone{RepositoryID: repositoryID, Format: FormatNPM, Coordinate: coordinate, Digest: item.Digest, TombstonedAt: now}
	return cloneNPMVersion(item), nil
}

func (s *MemoryStore) RestoreNPMVersion(ctx context.Context, repositoryID, name, version string) (NPMVersion, error) {
	key := npmVersionKey(repositoryID, name, version)
	s.mu.RLock()
	item, ok := s.npmVersions[key]
	s.mu.RUnlock()
	if !ok || item.State != "deleted" {
		return NPMVersion{}, ErrNotFound
	}
	release, err := s.LockNPMObject(ctx, item.ObjectKey)
	if err != nil {
		return NPMVersion{}, err
	}
	defer release()

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok = s.npmVersions[key]
	if !ok || item.State != "deleted" {
		return NPMVersion{}, ErrNotFound
	}
	if !item.CollectedAt.IsZero() {
		return NPMVersion{}, ErrDisabled
	}
	item.State, item.DeletedAt = "visible", time.Time{}
	s.npmVersions[key] = item
	packageKey := npmPackageKey(repositoryID, name)
	pkg := s.npmPackages[packageKey]
	if pkg.DistTags == nil {
		pkg.DistTags = make(map[string]string)
	}
	if pkg.DistTags["latest"] == "" {
		pkg.DistTags["latest"] = version
	}
	pkg.UpdatedAt = time.Now().UTC()
	s.npmPackages[packageKey] = pkg
	delete(s.artifactTombstones, repositoryID+"\x00"+string(FormatNPM)+"\x00"+name+"@"+version)
	return cloneNPMVersion(item), nil
}

func (s *MemoryStore) ListReclaimableNPMObjects(_ context.Context, before time.Time, limit int) ([]NPMObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	visible := make(map[string]bool)
	for _, version := range s.npmVersions {
		if version.State != "deleted" && version.ObjectKey != "" {
			visible[version.ObjectKey] = true
		}
	}
	objects := make([]NPMObject, 0)
	seen := make(map[string]bool)
	for _, version := range s.npmVersions {
		if len(objects) >= limit || version.State != "deleted" || version.ObjectKey == "" || visible[version.ObjectKey] || seen[version.ObjectKey] || !version.CollectedAt.IsZero() || version.DeletedAt.IsZero() || !version.DeletedAt.Before(before) {
			continue
		}
		seen[version.ObjectKey] = true
		objects = append(objects, NPMObject{RepositoryID: version.RepositoryID, ObjectKey: version.ObjectKey, Digest: version.Digest, Size: version.Size, DeletedAt: version.DeletedAt})
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].ObjectKey < objects[j].ObjectKey })
	return objects, nil
}

func (s *MemoryStore) NPMObjectHasVisibleReference(_ context.Context, objectKey string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, version := range s.npmVersions {
		if version.ObjectKey == objectKey && version.State != "deleted" {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) MarkNPMObjectCollected(_ context.Context, objectKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, version := range s.npmVersions {
		if version.ObjectKey == objectKey && version.State != "deleted" {
			return ErrNameExists
		}
	}
	now := time.Now().UTC()
	updated := false
	for key, version := range s.npmVersions {
		if version.ObjectKey == objectKey && version.State == "deleted" && version.CollectedAt.IsZero() {
			version.CollectedAt = now
			s.npmVersions[key] = version
			updated = true
		}
	}
	if !updated {
		return ErrNotFound
	}
	return nil
}

func (s *MemoryStore) GetNPMVersionByTarball(_ context.Context, repositoryID, name, tarballName string) (NPMVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, version := range s.npmVersions {
		if version.RepositoryID == repositoryID && version.PackageName == name && version.TarballName == tarballName && version.State != "deleted" {
			return cloneNPMVersion(version), nil
		}
	}
	return NPMVersion{}, ErrNotFound
}

func (s *MemoryStore) SearchNPMPackages(_ context.Context, repositoryID, prefix string, limit int, after string) ([]NPMPackageSummary, error) {
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	summaries := make([]NPMPackageSummary, 0)
	for _, pkg := range s.npmPackages {
		if pkg.RepositoryID != repositoryID || pkg.Negative || !strings.HasPrefix(pkg.Name, prefix) || pkg.Name <= after {
			continue
		}
		summary := NPMPackageSummary{
			RepositoryID: pkg.RepositoryID,
			Name:         pkg.Name,
			CreatedAt:    pkg.CreatedAt,
			UpdatedAt:    pkg.UpdatedAt,
		}
		for _, version := range s.npmVersions {
			if version.RepositoryID != repositoryID || version.PackageName != pkg.Name || version.State == "deleted" {
				continue
			}
			summary.VersionCount++
			latestTag := pkg.DistTags["latest"]
			currentIsLatestTag := latestTag != "" && summary.Latest.Version == latestTag
			if version.Version == latestTag || !currentIsLatestTag && (summary.Latest.Version == "" || version.CreatedAt.After(summary.Latest.CreatedAt)) {
				summary.Latest = cloneNPMVersion(version)
			}
		}
		if summary.VersionCount == 0 {
			continue
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, nil
}

func cloneNPMVersion(version NPMVersion) NPMVersion {
	version.Manifest = append([]byte(nil), version.Manifest...)
	return version
}

func cloneNPMPackage(pkg NPMPackage) NPMPackage {
	pkg.DistTags = cloneStringMap(pkg.DistTags)
	pkg.Versions = nil
	return pkg
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
