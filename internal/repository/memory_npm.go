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
		if version.RepositoryID == repositoryID && version.PackageName == name {
			pkg.Versions = append(pkg.Versions, cloneNPMVersion(version))
		}
	}
	sort.Slice(pkg.Versions, func(i, j int) bool {
		if !pkg.Versions[i].CreatedAt.Equal(pkg.Versions[j].CreatedAt) {
			return pkg.Versions[i].CreatedAt.After(pkg.Versions[j].CreatedAt)
		}
		return pkg.Versions[i].Version > pkg.Versions[j].Version
	})
	return pkg, nil
}

func (s *MemoryStore) GetNPMVersionByTarball(_ context.Context, repositoryID, name, tarballName string) (NPMVersion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, version := range s.npmVersions {
		if version.RepositoryID == repositoryID && version.PackageName == name && version.TarballName == tarballName {
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
			if version.RepositoryID != repositoryID || version.PackageName != pkg.Name {
				continue
			}
			summary.VersionCount++
			latestTag := pkg.DistTags["latest"]
			currentIsLatestTag := latestTag != "" && summary.Latest.Version == latestTag
			if version.Version == latestTag || !currentIsLatestTag && (summary.Latest.Version == "" || version.CreatedAt.After(summary.Latest.CreatedAt)) {
				summary.Latest = cloneNPMVersion(version)
			}
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
