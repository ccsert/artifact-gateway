package repository

import (
	"context"
	"sort"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func (s *MemoryStore) cargoPublicationTombstonedLocked(publication CargoPublication) bool {
	_, exists := s.artifactTombstones[cargoTombstoneKey(publication.RepositoryID, publication.Name, publication.Version)]
	return exists
}

func (s *MemoryStore) cargoPublicationForLifecycle(repositoryID, name, version string) (string, error) {
	identity, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return "", ErrInvalidCargoIdentity
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	publication, exists := s.cargoPublications[cargoVersionReservationKey(repositoryID, identity.CollisionKey, identity.VersionKey)]
	if !exists {
		return "", ErrNotFound
	}
	return publication.ObjectKey, nil
}

func (s *MemoryStore) TombstoneCargoPublication(ctx context.Context, repositoryID, name, version string) (CargoPublication, error) {
	key, err := s.cargoPublicationForLifecycle(repositoryID, name, version)
	if err != nil {
		return CargoPublication{}, err
	}
	release, err := s.LockCargoObject(ctx, key)
	if err != nil {
		return CargoPublication{}, err
	}
	defer release()
	identity, _ := cargo.NormalizeIdentity(name, version)
	s.mu.Lock()
	defer s.mu.Unlock()
	publication, exists := s.cargoPublications[cargoVersionReservationKey(repositoryID, identity.CollisionKey, identity.VersionKey)]
	if !exists || publication.ObjectKey != key || !publication.CollectedAt.IsZero() || s.cargoPublicationTombstonedLocked(publication) {
		return CargoPublication{}, ErrNotFound
	}
	now := time.Now().UTC()
	s.artifactTombstones[cargoTombstoneKey(repositoryID, publication.Name, publication.Version)] = ArtifactTombstone{
		RepositoryID: repositoryID, Format: FormatCargo, Coordinate: cargoPublicationCoordinate(publication.Name, publication.Version),
		Digest: publication.Digest, TombstonedAt: now,
	}
	return cloneCargoPublication(publication), nil
}

func (s *MemoryStore) RestoreCargoPublication(ctx context.Context, repositoryID, name, version string) (CargoPublication, error) {
	key, err := s.cargoPublicationForLifecycle(repositoryID, name, version)
	if err != nil {
		if err == ErrNotFound {
			return CargoPublication{}, ErrDisabled
		}
		return CargoPublication{}, err
	}
	release, err := s.LockCargoObject(ctx, key)
	if err != nil {
		return CargoPublication{}, err
	}
	defer release()
	identity, _ := cargo.NormalizeIdentity(name, version)
	s.mu.Lock()
	defer s.mu.Unlock()
	publication, exists := s.cargoPublications[cargoVersionReservationKey(repositoryID, identity.CollisionKey, identity.VersionKey)]
	if !exists || publication.ObjectKey != key || !publication.CollectingAt.IsZero() || !publication.CollectedAt.IsZero() {
		return CargoPublication{}, ErrDisabled
	}
	tombstoneKey := cargoTombstoneKey(repositoryID, publication.Name, publication.Version)
	if _, exists := s.artifactTombstones[tombstoneKey]; !exists {
		return CargoPublication{}, ErrNotFound
	}
	delete(s.artifactTombstones, tombstoneKey)
	return cloneCargoPublication(publication), nil
}

func (s *MemoryStore) ListReclaimableCargoObjects(ctx context.Context, before time.Time, limit int, after string) ([]CargoReclaimableObject, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	byKey := make(map[string]CargoReclaimableObject)
	for _, publication := range s.cargoPublications {
		if publication.ObjectKey <= after || !publication.CollectedAt.IsZero() {
			continue
		}
		tombstone, exists := s.artifactTombstones[cargoTombstoneKey(publication.RepositoryID, publication.Name, publication.Version)]
		if !exists {
			continue
		}
		object, exists := byKey[publication.ObjectKey]
		if !exists || tombstone.TombstonedAt.After(object.TombstonedAt) {
			byKey[publication.ObjectKey] = CargoReclaimableObject{RepositoryID: publication.RepositoryID, ObjectKey: publication.ObjectKey,
				Digest: publication.Digest, Size: publication.Size, TombstonedAt: tombstone.TombstonedAt}
		}
	}
	objects := make([]CargoReclaimableObject, 0, len(byKey))
	for _, object := range byKey {
		if !object.TombstonedAt.Before(before) {
			continue
		}
		jobKey := CargoTombstoneReclaimKey(object.ObjectKey, object.TombstonedAt)
		scheduled := false
		for _, job := range s.lifecycleJobs {
			if job.Kind == LifecycleJobReclaim && job.IdempotencyKey == jobKey {
				scheduled = true
				break
			}
		}
		if !scheduled {
			objects = append(objects, object)
		}
	}
	sort.Slice(objects, func(i, j int) bool { return objects[i].ObjectKey < objects[j].ObjectKey })
	if len(objects) > limit {
		objects = objects[:limit]
	}
	return objects, nil
}

func (s *MemoryStore) CargoObjectHasVisibleReference(ctx context.Context, objectKey string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, publication := range s.cargoPublications {
		if publication.ObjectKey == objectKey && publication.CollectedAt.IsZero() && !s.cargoPublicationTombstonedLocked(publication) {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) CargoObjectMatchesTombstone(ctx context.Context, objectKey string, expected time.Time) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest time.Time
	for _, publication := range s.cargoPublications {
		if publication.ObjectKey == objectKey && publication.CollectedAt.IsZero() {
			if tombstone, exists := s.artifactTombstones[cargoTombstoneKey(publication.RepositoryID, publication.Name, publication.Version)]; exists && tombstone.TombstonedAt.After(latest) {
				latest = tombstone.TombstonedAt
			}
		}
	}
	return !latest.IsZero() && latest.Equal(expected), nil
}

func (s *MemoryStore) MarkCargoObjectCollecting(ctx context.Context, objectKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	updated := false
	for key, publication := range s.cargoPublications {
		if publication.ObjectKey == objectKey && publication.CollectedAt.IsZero() && s.cargoPublicationTombstonedLocked(publication) {
			if publication.CollectingAt.IsZero() {
				publication.CollectingAt = now
				s.cargoPublications[key] = publication
			}
			updated = true
		}
	}
	if !updated {
		return ErrNotFound
	}
	return nil
}

func (s *MemoryStore) MarkCargoObjectCollected(ctx context.Context, objectKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	updated := false
	for key, publication := range s.cargoPublications {
		if publication.ObjectKey == objectKey && publication.CollectedAt.IsZero() && s.cargoPublicationTombstonedLocked(publication) {
			publication.CollectingAt = time.Time{}
			publication.CollectedAt = now
			s.cargoPublications[key] = publication
			updated = true
		}
	}
	if !updated {
		return ErrNotFound
	}
	return nil
}
