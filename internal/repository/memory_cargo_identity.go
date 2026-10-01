package repository

import (
	"context"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func (s *MemoryStore) ReserveCargoIdentity(ctx context.Context, claim CargoIdentityClaim) (CargoIdentityReservation, bool, error) {
	reservation, err := normalizeCargoIdentityClaim(claim)
	if err != nil {
		return CargoIdentityReservation{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return CargoIdentityReservation{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hostedRepositories[claim.RepositoryID]; !ok {
		return CargoIdentityReservation{}, false, ErrNotFound
	}
	nameKey := cargoNameReservationKey(claim.RepositoryID, reservation.CollisionKey)
	if existing, ok := s.cargoNames[nameKey]; ok && existing != claim.Name {
		return CargoIdentityReservation{}, false, ErrCargoIdentityConflict
	}
	versionKey := cargoVersionReservationKey(claim.RepositoryID, reservation.CollisionKey, reservation.VersionKey)
	if existing, ok := s.cargoReservations[versionKey]; ok {
		if existing.Name != claim.Name || existing.Version != claim.Version || existing.Digest != claim.Digest || existing.MetadataDigest != claim.MetadataDigest {
			return CargoIdentityReservation{}, false, ErrCargoIdentityConflict
		}
		return existing, true, nil
	}
	if s.cargoNames == nil {
		s.cargoNames = make(map[string]string)
	}
	if s.cargoReservations == nil {
		s.cargoReservations = make(map[string]CargoIdentityReservation)
	}
	reservation.CreatedAt = time.Now().UTC()
	s.cargoNames[nameKey] = claim.Name
	s.cargoReservations[versionKey] = reservation
	return reservation, false, nil
}

func (s *MemoryStore) GetCargoIdentityReservation(ctx context.Context, repositoryID, name, version string) (CargoIdentityReservation, error) {
	if err := ctx.Err(); err != nil {
		return CargoIdentityReservation{}, err
	}
	normalized, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return CargoIdentityReservation{}, ErrInvalidCargoIdentity
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.cargoReservations[cargoVersionReservationKey(repositoryID, normalized.CollisionKey, normalized.VersionKey)]
	if !ok {
		return CargoIdentityReservation{}, ErrNotFound
	}
	return item, nil
}
