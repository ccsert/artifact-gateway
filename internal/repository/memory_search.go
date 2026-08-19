package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SearchArtifactProjection mirrors the PostgreSQL management projection so
// local mode and tests exercise the same query and pagination semantics.
func (s *MemoryStore) SearchArtifactProjection(_ context.Context, repositoryID string, format Format, query ArtifactSearchQuery, limit int, after ArtifactSearchPosition) ([]ArtifactSearchItem, error) {
	if query.Mode != ArtifactSearchByCoordinate && query.Mode != ArtifactSearchByDigest {
		return nil, fmt.Errorf("unsupported artifact search mode %q", query.Mode)
	}
	if limit <= 0 {
		limit = 100
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	items := s.artifactSearchItemsLocked(repositoryID, format)
	for index := range items {
		if items[index].Digest == "" {
			continue
		}
		if value, ok := s.artifactIntelligence[artifactIntelligenceKey(repositoryID, format, items[index].Coordinate, items[index].Digest)]; ok {
			items[index].Intelligence = summarizeArtifactIntelligence(value)
		}
	}
	matched := make([]ArtifactSearchItem, 0, len(items))
	for _, item := range items {
		if query.Mode == ArtifactSearchByCoordinate && !strings.HasPrefix(item.Coordinate, query.Value) {
			continue
		}
		if query.Mode == ArtifactSearchByDigest && item.Digest != query.Value {
			continue
		}
		matched = append(matched, item)
	}
	if format != FormatMaven {
		matched = collapseArtifactSearchItems(matched, query.Mode)
	}
	sort.Slice(matched, func(i, j int) bool {
		return artifactSearchItemLess(matched[i], matched[j])
	})

	start := sort.Search(len(matched), func(index int) bool {
		return artifactSearchItemAfter(matched[index], after)
	})
	matched = matched[start:]
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, nil
}

func summarizeArtifactIntelligence(value ArtifactIntelligence) *ArtifactIntelligenceSummary {
	summary := &ArtifactIntelligenceSummary{
		SignatureCount: len(value.Signatures),
		SBOMCount:      len(value.SBOMs),
		LicenseCount:   len(value.Licenses),
	}
	if value.Vulnerability != nil {
		summary.VulnerabilityStatus = value.Vulnerability.Status
		summary.Critical = value.Vulnerability.Critical
		summary.High = value.Vulnerability.High
		summary.Medium = value.Vulnerability.Medium
		summary.Low = value.Vulnerability.Low
		summary.Unknown = value.Vulnerability.Unknown
	}
	return summary
}

func (s *MemoryStore) artifactSearchItemsLocked(repositoryID string, format Format) []ArtifactSearchItem {
	items := make([]ArtifactSearchItem, 0)
	switch format {
	case FormatMaven:
		for _, artifact := range s.mavenArtifacts {
			if artifact.RepositoryID != repositoryID || artifact.State != "visible" {
				continue
			}
			createdAt := artifact.CreatedAt
			items = append(items, ArtifactSearchItem{
				Coordinate: artifact.Coordinate, Digest: artifact.Digest, CreatedAt: &createdAt,
				Publisher: s.latestCommittedMavenPublisherLocked(repositoryID, artifact.Coordinate), BuildNumber: artifact.BuildNumber,
			})
		}
	case FormatOCI:
		for _, manifest := range s.ociManifests {
			if manifest.RepositoryID != repositoryID {
				continue
			}
			createdAt, size := manifest.CreatedAt, manifest.Size
			items = append(items, ArtifactSearchItem{
				Coordinate: manifest.Name, Digest: manifest.Digest, CreatedAt: &createdAt,
				Size: &size, ContentType: manifest.MediaType,
			})
		}
	case FormatConan:
		for _, revision := range s.conanRecipes {
			if revision.RepositoryID != repositoryID || revision.State != "visible" {
				continue
			}
			createdAt := revision.CreatedAt
			items = append(items, ArtifactSearchItem{
				Coordinate: revision.Reference, Digest: revision.Digest, CreatedAt: &createdAt,
				Publisher: s.latestCommittedConanPublisherLocked(repositoryID, revision.Reference),
			})
		}
	case FormatRaw:
		for _, asset := range s.rawAssets {
			if asset.RepositoryID != repositoryID {
				continue
			}
			createdAt, size := asset.UpdatedAt, asset.Size
			items = append(items, ArtifactSearchItem{
				Coordinate: asset.Path, Digest: asset.Digest, CreatedAt: &createdAt,
				Size: &size, ContentType: asset.ContentType,
			})
		}
	case FormatNPM:
		for _, version := range s.npmVersions {
			if version.RepositoryID != repositoryID || version.State != "visible" {
				continue
			}
			createdAt, size := version.CreatedAt, version.Size
			items = append(items, ArtifactSearchItem{
				Coordinate: version.PackageName, Version: version.Version, Digest: version.Digest,
				CreatedAt: &createdAt, Size: &size, Publisher: version.Publisher,
				ContentType: "application/octet-stream",
			})
		}
	case FormatPyPI:
		for _, file := range s.pypiFiles {
			if file.RepositoryID != repositoryID || file.State != "visible" {
				continue
			}
			createdAt, size := file.CreatedAt, file.Size
			items = append(items, ArtifactSearchItem{
				Coordinate: file.Project, Version: file.Version, Digest: file.Digest,
				CreatedAt: &createdAt, Size: &size, Publisher: file.Publisher,
				ContentType: "application/octet-stream",
			})
		}
	case FormatGo:
		for _, version := range s.goVersions {
			if version.RepositoryID != repositoryID || s.goModuleVersionTombstonedLocked(repositoryID, version.Module, version.Version) {
				continue
			}
			createdAt := version.CreatedAt
			item := ArtifactSearchItem{Coordinate: version.Module, Version: version.Version, CreatedAt: &createdAt, Publisher: version.Publisher}
			for _, kind := range []string{"zip", "mod", "info"} {
				asset, ok := s.goAssets[goAssetKey(repositoryID, version.Module, version.Version, kind)]
				if !ok {
					continue
				}
				size := asset.Size
				item.Digest, item.Size = asset.Digest, &size
				item.ContentType = "text/plain"
				if kind == "zip" {
					item.ContentType = "application/zip"
				}
				break
			}
			items = append(items, item)
		}
	case FormatAPT:
		for _, asset := range s.aptAssets {
			if asset.RepositoryID != repositoryID {
				continue
			}
			createdAt, size := asset.CreatedAt, asset.Size
			items = append(items, ArtifactSearchItem{Coordinate: asset.Path, Digest: asset.Digest, CreatedAt: &createdAt, Size: &size, ContentType: asset.ContentType})
		}
		for snapshotID, assets := range s.aptSnapshotAssets {
			snapshot := s.aptSnapshots[snapshotID]
			if snapshot.RepositoryID != repositoryID || snapshot.State != APTRepositorySnapshotVisible {
				continue
			}
			for _, asset := range assets {
				createdAt, size := snapshot.CreatedAt, asset.Size
				items = append(items, ArtifactSearchItem{Coordinate: asset.Path, Digest: asset.Digest, CreatedAt: &createdAt, Size: &size, ContentType: asset.ContentType})
			}
		}
	}
	return items
}

func collapseArtifactSearchItems(items []ArtifactSearchItem, mode ArtifactSearchMode) []ArtifactSearchItem {
	byIdentity := make(map[string]ArtifactSearchItem, len(items))
	for _, item := range items {
		key := item.Coordinate
		if mode == ArtifactSearchByDigest {
			key += "\x00" + item.Digest
		}
		current, exists := byIdentity[key]
		if !exists || artifactSearchItemNewer(item, current) {
			byIdentity[key] = item
		}
	}
	collapsed := make([]ArtifactSearchItem, 0, len(byIdentity))
	for _, item := range byIdentity {
		collapsed = append(collapsed, item)
	}
	return collapsed
}

func artifactSearchItemNewer(left, right ArtifactSearchItem) bool {
	leftTime, rightTime := time.Time{}, time.Time{}
	if left.CreatedAt != nil {
		leftTime = *left.CreatedAt
	}
	if right.CreatedAt != nil {
		rightTime = *right.CreatedAt
	}
	return leftTime.After(rightTime) || leftTime.Equal(rightTime) && left.Digest > right.Digest
}

func artifactSearchItemLess(left, right ArtifactSearchItem) bool {
	if left.Coordinate != right.Coordinate {
		return left.Coordinate < right.Coordinate
	}
	if left.BuildNumber != right.BuildNumber {
		return left.BuildNumber < right.BuildNumber
	}
	return left.Digest < right.Digest
}

func artifactSearchItemAfter(item ArtifactSearchItem, after ArtifactSearchPosition) bool {
	return item.Coordinate > after.Coordinate ||
		item.Coordinate == after.Coordinate && item.BuildNumber > after.BuildNumber ||
		after.Digest != "" && item.Coordinate == after.Coordinate && item.BuildNumber == after.BuildNumber && item.Digest > after.Digest
}
