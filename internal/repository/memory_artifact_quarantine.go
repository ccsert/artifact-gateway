package repository

import (
	"context"
	"time"
)

func (s *MemoryStore) GetArtifactQuarantine(_ context.Context, repositoryID string, format Format, coordinate, digest string) (ArtifactQuarantine, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.artifactQuarantines[artifactQuarantineKey(repositoryID, format, coordinate, digest)]
	if !ok {
		return ArtifactQuarantine{}, ErrNotFound
	}
	return value, nil
}

func (s *MemoryStore) ReplaceArtifactQuarantine(_ context.Context, value ArtifactQuarantine, expectedVersion string) (ArtifactQuarantine, error) {
	if !validArtifactQuarantine(value) {
		return ArtifactQuarantine{}, ErrInvalidArtifactQuarantine
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := artifactQuarantineKey(value.RepositoryID, value.Format, value.Coordinate, value.Digest)
	current, exists := s.artifactQuarantines[key]
	if !exists {
		if expectedVersion != "0" {
			return ArtifactQuarantine{}, ErrNotFound
		}
		if value.State != ArtifactQuarantineStateQuarantined {
			return ArtifactQuarantine{}, ErrInvalidArtifactQuarantine
		}
		now := time.Now().UTC()
		value.Version = "1"
		value.QuarantinedAt = now
		value.ReleasedAt = time.Time{}
		value.UpdatedAt = now
		s.artifactQuarantines[key] = value
		s.enqueueWebhookEventLocked(artifactQuarantineWebhookEvent(value))
		return value, nil
	}
	if current.Version != expectedVersion {
		return ArtifactQuarantine{}, ErrVersionConflict
	}
	value.Version = nextHostedGroupVersion(current.Version)
	if value.State == ArtifactQuarantineStateQuarantined {
		value.QuarantinedAt = current.QuarantinedAt
		if current.State != ArtifactQuarantineStateQuarantined {
			value.QuarantinedAt = time.Now().UTC()
		}
		value.ReleasedAt = time.Time{}
	} else {
		value.QuarantinedAt = current.QuarantinedAt
		value.ReleasedAt = time.Now().UTC()
	}
	value.UpdatedAt = time.Now().UTC()
	s.artifactQuarantines[key] = value
	s.enqueueWebhookEventLocked(artifactQuarantineWebhookEvent(value))
	return value, nil
}
