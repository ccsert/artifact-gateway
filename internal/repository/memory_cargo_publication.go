package repository

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func (s *MemoryStore) CommitCargoPublication(ctx context.Context, incoming CargoPublication) (CargoPublication, bool, error) {
	publication, normalized, err := normalizeCargoPublication(incoming)
	if err != nil {
		return CargoPublication{}, false, err
	}
	if err = ctx.Err(); err != nil {
		return CargoPublication{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[publication.RepositoryID]
	if !ok || repo.Format != FormatCargo || repo.Type != RepositoryTypeHosted || repo.State != RepositoryActive {
		return CargoPublication{}, false, ErrNotFound
	}
	key := cargoVersionReservationKey(publication.RepositoryID, normalized.CollisionKey, normalized.VersionKey)
	reservation, ok := s.cargoReservations[key]
	if !ok || !cargoReservationMatches(reservation, publication) {
		return CargoPublication{}, false, ErrCargoIdentityConflict
	}
	if existing, ok := s.cargoPublications[key]; ok {
		if !cargoPublicationMatches(existing, publication) {
			return CargoPublication{}, false, ErrCargoPublicationConflict
		}
		return cloneCargoPublication(existing), true, nil
	}
	for _, group := range s.hostedGroups {
		if group.Format != FormatCargo {
			continue
		}
		member := false
		for _, source := range group.Members {
			if source.RepositoryID == publication.RepositoryID {
				member = true
				break
			}
		}
		if !member {
			continue
		}
		ownerKey, err := cargoProxyCrateKey(group.ID, publication.Name, publication.Version)
		if err != nil {
			return CargoPublication{}, false, err
		}
		if owner, found := s.cargoGroupVersions[ownerKey]; found && !cargoGroupPublicationCompatible(owner, publication) {
			return CargoPublication{}, false, ErrCargoGroupConflict
		}
		if err := s.preflightCargoGroupMembersLocked(group.ID, group.Members,
			cargoGroupKnownIndex{RepositoryID: publication.RepositoryID, Body: publication.IndexRow}); err != nil {
			return CargoPublication{}, false, err
		}
	}
	capacity, err := s.repositoryCapacityLocked(repo.ID)
	if err != nil {
		return CargoPublication{}, false, err
	}
	if capacity.QuotaBytes > 0 && (capacity.UsedBytes >= capacity.QuotaBytes || publication.Size > capacity.QuotaBytes-capacity.UsedBytes) {
		return CargoPublication{}, false, ErrQuotaExceeded
	}
	if s.cargoPublications == nil {
		s.cargoPublications = make(map[string]CargoPublication)
	}
	publication.CreatedAt = time.Now().UTC()
	publication.UpdatedAt = publication.CreatedAt
	publication.IndexRow = append([]byte(nil), publication.IndexRow...)
	s.cargoPublications[key] = publication
	return cloneCargoPublication(publication), false, nil
}

func (s *MemoryStore) SetCargoYanked(ctx context.Context, repositoryID, name, version string, yanked bool) (CargoPublication, bool, error) {
	if err := ctx.Err(); err != nil {
		return CargoPublication{}, false, err
	}
	identity, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return CargoPublication{}, false, ErrInvalidCargoIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := cargoVersionReservationKey(repositoryID, identity.CollisionKey, identity.VersionKey)
	publication, ok := s.cargoPublications[key]
	if !ok {
		return CargoPublication{}, false, ErrNotFound
	}
	if publication.Yanked == yanked {
		return cloneCargoPublication(publication), false, nil
	}
	publication.Yanked = yanked
	publication.UpdatedAt = time.Now().UTC()
	s.cargoPublications[key] = publication
	return cloneCargoPublication(publication), true, nil
}

func (s *MemoryStore) GetCargoPublication(ctx context.Context, repositoryID, name, version string) (CargoPublication, error) {
	if err := ctx.Err(); err != nil {
		return CargoPublication{}, err
	}
	identity, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return CargoPublication{}, ErrInvalidCargoIdentity
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	publication, ok := s.cargoPublications[cargoVersionReservationKey(repositoryID, identity.CollisionKey, identity.VersionKey)]
	if !ok {
		return CargoPublication{}, ErrNotFound
	}
	return cloneCargoPublication(publication), nil
}

func (s *MemoryStore) ListCargoPublications(ctx context.Context, repositoryID, name string) ([]CargoPublication, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	identity, err := cargo.NormalizeIdentity(name, "0.0.0")
	if err != nil || repositoryID == "" {
		return nil, ErrInvalidCargoIdentity
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]CargoPublication, 0)
	prefix := cargoNameReservationKey(repositoryID, identity.CollisionKey) + "\x00"
	for key, publication := range s.cargoPublications {
		if strings.HasPrefix(key, prefix) {
			items = append(items, cloneCargoPublication(publication))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	return items, nil
}

func cloneCargoPublication(publication CargoPublication) CargoPublication {
	publication.IndexRow = append([]byte(nil), publication.IndexRow...)
	return publication
}

func (s *MemoryStore) LockCargoObject(_ context.Context, objectKey string) (func(), error) {
	s.mu.Lock()
	if s.cargoObjectLocks == nil {
		s.cargoObjectLocks = make(map[string]*sync.Mutex)
	}
	locks := s.cargoObjectLocks
	s.mu.Unlock()
	return s.lockMemoryObject(locks, objectKey)
}

func (s *MemoryStore) CargoObjectHasReference(ctx context.Context, objectKey string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, publication := range s.cargoPublications {
		if publication.ObjectKey == objectKey {
			return true, nil
		}
	}
	return false, nil
}
