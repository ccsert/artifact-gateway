package repository

import (
	"context"
	"time"
)

func (s *MemoryStore) UpdateNPMMetadata(ctx context.Context, mutation NPMMetadataMutation) (NPMMetadataChange, error) {
	if err := ctx.Err(); err != nil {
		return NPMMetadataChange{}, err
	}
	versions, err := mutation.selectedVersions()
	if err != nil {
		return NPMMetadataChange{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return NPMMetadataChange{}, err
	}
	key := npmPackageKey(mutation.RepositoryID, mutation.PackageName)
	pkg, ok := s.npmPackages[key]
	if !ok || pkg.SourceEndpoint != "" || pkg.Negative {
		return NPMMetadataChange{}, ErrNotFound
	}
	if pkg.Revision != mutation.ExpectedRevision {
		return NPMMetadataChange{}, ErrVersionConflict
	}
	selected := make(map[string]bool, len(versions))
	for _, version := range versions {
		item, exists := s.npmVersions[npmVersionKey(mutation.RepositoryID, mutation.PackageName, version)]
		if !exists || item.State != "visible" {
			return NPMMetadataChange{}, ErrNotFound
		}
		selected[version] = true
	}
	if mutation.Unpublish != nil {
		for _, target := range mutation.DistTags {
			item, exists := s.npmVersions[npmVersionKey(mutation.RepositoryID, mutation.PackageName, target)]
			if selected[target] || !exists || item.State != "visible" {
				return NPMMetadataChange{}, ErrNPMDistTagTargetUnavailable
			}
		}
	}
	change := NPMMetadataChange{Revision: pkg.Revision, Versions: versions}
	if mutation.Deprecations != nil {
		for _, version := range versions {
			versionKey := npmVersionKey(mutation.RepositoryID, mutation.PackageName, version)
			item := s.npmVersions[versionKey]
			if item.Deprecated != mutation.Deprecations[version] {
				item.Deprecated = mutation.Deprecations[version]
				s.npmVersions[versionKey] = item
				change.Changed = true
			}
		}
	} else {
		for _, version := range versions {
			// All targets were checked under this lock before any write.
			_, _ = s.tombstoneNPMVersionLocked(mutation.RepositoryID, mutation.PackageName, version)
		}
		pkg = s.npmPackages[key]
		pkg.DistTags = cloneStringMap(mutation.DistTags)
		change.Changed = true
	}
	if change.Changed {
		pkg.Revision++
		pkg.UpdatedAt = time.Now().UTC()
		s.npmPackages[key] = pkg
	}
	change.Revision = pkg.Revision
	s.appendAuditLocked(withRequestContextAudit(ctx, mutation.auditRecord(change)))
	return change, nil
}
