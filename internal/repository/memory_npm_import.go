package repository

import (
	"context"
	"math"
	"sort"
	"time"
)

func (s *MemoryStore) PublishNPMImport(ctx context.Context, c NPMImportClosure) error {
	if err := validateNPMImport(c); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	versions := append([]NPMVersion(nil), c.Versions...)
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
	releases := []func(){}
	defer func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}()
	for _, v := range versions {
		release, err := s.LockArtifactScanIdentity(ctx, c.RepositoryID, FormatNPM, c.PackageName+"@"+v.Version, v.Digest)
		if err != nil {
			return err
		}
		releases = append(releases, release)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[c.RepositoryID]
	if !ok || repo.Format != FormatNPM || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
		return ErrDisabled
	}
	key := npmPackageKey(c.RepositoryID, c.PackageName)
	pkg, exists := s.npmPackages[key]
	if exists && (pkg.SourceEndpoint != "" || pkg.Negative) {
		return ErrNameExists
	}
	if pkg.DistTags == nil {
		pkg.DistTags = map[string]string{}
	}
	for tag, want := range c.DistTags {
		if old, present := pkg.DistTags[tag]; present && old != want {
			return ErrNameExists
		}
	}
	capacity, err := s.repositoryCapacityLocked(c.RepositoryID)
	if err != nil {
		return err
	}
	additional := int64(0)
	for _, v := range c.Versions {
		if _, present := s.npmVersions[npmVersionKey(c.RepositoryID, c.PackageName, v.Version)]; !present {
			if v.Size > math.MaxInt64-additional {
				return ErrQuotaExceeded
			}
			additional += v.Size
		}
	}
	if capacity.QuotaBytes > 0 && (additional > math.MaxInt64-capacity.UsedBytes || capacity.UsedBytes+additional > capacity.QuotaBytes) {
		return ErrQuotaExceeded
	}
	changed := false
	for _, v := range c.Versions {
		if old, present := s.npmVersions[npmVersionKey(c.RepositoryID, c.PackageName, v.Version)]; present {
			if !NPMImportVersionMatches(old, v) {
				return ErrNameExists
			}
		} else {
			changed = true
		}
		for _, old := range s.npmVersions {
			if old.RepositoryID == c.RepositoryID && old.PackageName == c.PackageName && old.TarballName == v.TarballName && old.Version != v.Version {
				return ErrNameExists
			}
		}
		for _, blocked := range s.artifactQuarantines {
			if blocked.RepositoryID == c.RepositoryID && blocked.Format == FormatNPM && blocked.Coordinate == c.PackageName+"@"+v.Version && blocked.Digest == v.Digest && blocked.State == ArtifactQuarantineStateQuarantined {
				return ErrArtifactQuarantined
			}
		}
		for _, tombstone := range s.artifactTombstones {
			if tombstone.RepositoryID == c.RepositoryID && tombstone.Format == FormatNPM && tombstone.Coordinate == c.PackageName+"@"+v.Version {
				return ErrNameExists
			}
		}
	}
	now := time.Now().UTC()
	for _, v := range c.Versions {
		versionKey := npmVersionKey(c.RepositoryID, c.PackageName, v.Version)
		if _, present := s.npmVersions[versionKey]; !present {
			v.CreatedAt, v.CachedAt, v.State = now, now, "visible"
			s.npmVersions[versionKey] = cloneNPMVersion(v)
		}
	}
	for tag, version := range c.DistTags {
		if _, present := pkg.DistTags[tag]; !present {
			changed = true
			pkg.DistTags[tag] = version
		}
	}
	if !exists {
		pkg.RepositoryID, pkg.Name, pkg.CreatedAt = c.RepositoryID, c.PackageName, now
	}
	if changed {
		pkg.Revision++
		pkg.UpdatedAt = now
		s.npmPackages[key] = pkg
		s.appendAuditLocked(withRequestContextAudit(ctx, npmImportAudit(c, repo.Name)))
	}
	return nil
}
