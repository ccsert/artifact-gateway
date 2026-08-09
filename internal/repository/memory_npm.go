package repository

import (
	"context"
	"sort"
	"strings"
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
		if pkg.RepositoryID != repositoryID || !strings.HasPrefix(pkg.Name, prefix) || pkg.Name <= after {
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
	distTags := make(map[string]string, len(pkg.DistTags))
	for tag, version := range pkg.DistTags {
		distTags[tag] = version
	}
	pkg.DistTags = distTags
	pkg.Versions = nil
	return pkg
}
