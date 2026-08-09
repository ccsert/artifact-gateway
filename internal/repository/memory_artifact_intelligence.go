package repository

import (
	"context"
	"strings"
	"time"
)

func artifactIntelligenceKey(repositoryID string, format Format, coordinate, digest string) string {
	return strings.Join([]string{repositoryID, string(format), coordinate, digest}, "\x00")
}

func cloneArtifactIntelligence(value ArtifactIntelligence) ArtifactIntelligence {
	value.Signatures = append([]ArtifactSignature{}, value.Signatures...)
	value.SBOMs = append([]ArtifactSBOM{}, value.SBOMs...)
	value.Licenses = append([]ArtifactLicense{}, value.Licenses...)
	if value.Provenance != nil {
		copy := *value.Provenance
		value.Provenance = &copy
	}
	if value.Vulnerability != nil {
		copy := *value.Vulnerability
		value.Vulnerability = &copy
	}
	return value
}

func (s *MemoryStore) GetArtifactIntelligence(_ context.Context, repositoryID string, format Format, coordinate, digest string) (ArtifactIntelligence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.artifactIntelligence[artifactIntelligenceKey(repositoryID, format, coordinate, digest)]
	if !ok {
		return ArtifactIntelligence{}, ErrNotFound
	}
	return cloneArtifactIntelligence(value), nil
}

func (s *MemoryStore) ReplaceArtifactIntelligence(_ context.Context, value ArtifactIntelligence, expectedVersion string) (ArtifactIntelligence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := artifactIntelligenceKey(value.RepositoryID, value.Format, value.Coordinate, value.Digest)
	current, exists := s.artifactIntelligence[key]
	if exists && current.Version != expectedVersion {
		return ArtifactIntelligence{}, ErrVersionConflict
	}
	if !exists && expectedVersion != "" && expectedVersion != "0" {
		return ArtifactIntelligence{}, ErrVersionConflict
	}
	now := time.Now().UTC()
	if !exists {
		value.Version = "1"
		value.CreatedAt = now
	} else {
		value.Version = nextHostedGroupVersion(current.Version)
		value.CreatedAt = current.CreatedAt
	}
	value.UpdatedAt = now
	value = cloneArtifactIntelligence(value)
	s.artifactIntelligence[key] = value
	return cloneArtifactIntelligence(value), nil
}
