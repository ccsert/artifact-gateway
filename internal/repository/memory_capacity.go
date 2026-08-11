package repository

import (
	"context"
	"sort"
)

func (s *MemoryStore) GetRepositoryCapacity(_ context.Context, id string) (RepositoryCapacity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.repositoryCapacityLocked(id)
}

func (s *MemoryStore) repositoryCapacityLocked(id string) (RepositoryCapacity, error) {
	repo, ok := s.hostedRepositories[id]
	if !ok {
		return RepositoryCapacity{}, ErrNotFound
	}
	capacity := RepositoryCapacity{RepositoryID: id, Format: repo.Format, QuotaBytes: s.capacityQuotas[id]}
	switch repo.Format {
	case FormatRaw:
		for _, asset := range s.rawAssets {
			if asset.RepositoryID == id {
				capacity.UsedBytes += asset.Size
				capacity.ObjectCount++
			}
		}
	case FormatMaven:
		for _, asset := range s.mavenAssets {
			if asset.RepositoryID == id && s.mavenAssetVisibleLocked(asset) {
				capacity.UsedBytes += asset.Size
				capacity.ObjectCount++
			}
		}
	case FormatOCI:
		for _, manifest := range s.ociManifests {
			if manifest.RepositoryID == id {
				capacity.UsedBytes += manifest.Size
				capacity.ObjectCount++
			}
		}
		for digest := range s.ociRepositoryBlobs[id] {
			if blob, ok := s.ociBlobs[digest]; ok {
				capacity.UsedBytes += blob.Size
				capacity.ObjectCount++
			}
		}
	case FormatConan:
		for _, asset := range s.conanAssets {
			if asset.RepositoryID == id && s.conanObjectVisibleLocked(asset.ObjectKey) {
				capacity.UsedBytes += asset.Size
				capacity.ObjectCount++
			}
		}
	case FormatNPM:
		for _, version := range s.npmVersions {
			if version.RepositoryID == id && version.ObjectKey != "" {
				capacity.UsedBytes += version.Size
				capacity.ObjectCount++
			}
		}
	case FormatPyPI:
		for _, file := range s.pypiFiles {
			if file.RepositoryID == id && file.State == "visible" && file.ObjectKey != "" {
				capacity.UsedBytes += file.Size
				capacity.ObjectCount++
			}
		}
	case FormatGo:
		for _, asset := range s.goAssets {
			if asset.RepositoryID == id {
				capacity.UsedBytes += asset.Size
				capacity.ObjectCount++
			}
		}
	case FormatAPT:
		for _, asset := range s.aptAssets {
			if asset.RepositoryID == id {
				capacity.UsedBytes += asset.Size
				capacity.ObjectCount++
			}
		}
	}
	return capacity, nil
}

func (s *MemoryStore) ListRepositoryCapacityRecords(_ context.Context) ([]RepositoryCapacityRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	records := make([]RepositoryCapacityRecord, 0, len(s.hostedRepositories))
	for id, repo := range s.hostedRepositories {
		capacity, err := s.repositoryCapacityLocked(id)
		if err != nil {
			return nil, err
		}
		records = append(records, RepositoryCapacityRecord{Repository: repo, Capacity: capacity})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Repository.ID < records[j].Repository.ID })
	return records, nil
}

func (s *MemoryStore) mavenAssetVisibleLocked(asset MavenAsset) bool {
	for _, artifact := range s.mavenArtifacts {
		if artifact.RepositoryID == asset.RepositoryID && artifact.State == "visible" && mavenAssetBelongsToArtifact(asset, artifact.Coordinate) {
			return true
		}
	}
	return false
}

func (s *MemoryStore) ReplaceRepositoryCapacityQuota(ctx context.Context, id string, quota int64) (RepositoryCapacity, error) {
	if quota < 0 {
		return RepositoryCapacity{}, ErrDisabled
	}
	s.mu.Lock()
	if _, ok := s.hostedRepositories[id]; !ok {
		s.mu.Unlock()
		return RepositoryCapacity{}, ErrNotFound
	}
	s.capacityQuotas[id] = quota
	s.mu.Unlock()
	return s.GetRepositoryCapacity(ctx, id)
}
