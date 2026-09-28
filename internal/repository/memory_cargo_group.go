package repository

import (
	"bytes"
	"context"
	"strings"
)

func (s *MemoryStore) ReconcileCargoGroupIndex(ctx context.Context, groupID, name string, candidates []CargoGroupVersion) ([]CargoGroupVersion, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, err := cargoProxyNameKey(groupID, name)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	group, exists := s.hostedGroups[groupID]
	if !exists || group.Format != FormatCargo {
		return nil, ErrNotFound
	}
	members := make(map[string]bool)
	for _, member := range group.Members {
		repo, found := s.hostedRepositories[member.RepositoryID]
		if found && repo.Format == FormatCargo && repo.State == RepositoryActive {
			members[repo.ID] = true
		}
	}
	old := make([]CargoGroupVersion, 0)
	for savedKey, value := range s.cargoGroupVersions {
		if strings.HasPrefix(savedKey, key+"\x00") {
			old = append(old, value)
		}
	}
	selected, err := reconcileCargoGroupVersions(groupID, name, candidates, old, members)
	if err != nil {
		return nil, err
	}
	if s.cargoGroupVersions == nil {
		s.cargoGroupVersions = make(map[string]CargoGroupVersion)
	}
	for _, value := range selected {
		versionKey, keyErr := cargoProxyCrateKey(groupID, value.Name, value.Version)
		if keyErr != nil {
			return nil, keyErr
		}
		value.IndexRow = bytes.Clone(value.IndexRow)
		s.cargoGroupVersions[versionKey] = value
	}
	return selected, nil
}

func (s *MemoryStore) GetCargoGroupVersion(ctx context.Context, groupID, name, version string) (CargoGroupVersion, error) {
	if err := ctx.Err(); err != nil {
		return CargoGroupVersion{}, err
	}
	key, err := cargoProxyCrateKey(groupID, name, version)
	if err != nil {
		return CargoGroupVersion{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, found := s.cargoGroupVersions[key]
	if !found {
		return CargoGroupVersion{}, ErrNotFound
	}
	value.IndexRow = bytes.Clone(value.IndexRow)
	return value, nil
}

// Call with s.mu held by the group mutation. This keeps membership changes
// atomic with Hosted publication and Proxy cache writes in the memory store.
func (s *MemoryStore) preflightCargoGroupMembersLocked(groupID string, members []GroupMember, additional ...cargoGroupKnownIndex) error {
	memberIDs := make(map[string]bool, len(members))
	for _, member := range members {
		memberIDs[member.RepositoryID] = true
	}
	owners := make([]CargoGroupVersion, 0)
	for _, owner := range s.cargoGroupVersions {
		if owner.GroupID == groupID {
			owners = append(owners, owner)
		}
	}
	indexes := make([]cargoGroupKnownIndex, 0)
	for _, publication := range s.cargoPublications {
		if memberIDs[publication.RepositoryID] {
			indexes = append(indexes, cargoGroupKnownIndex{RepositoryID: publication.RepositoryID, Body: publication.IndexRow})
		}
	}
	for _, index := range s.cargoProxyIndexes {
		if memberIDs[index.RepositoryID] && index.Status == 200 {
			indexes = append(indexes, cargoGroupKnownIndex{RepositoryID: index.RepositoryID, Body: index.Body})
		}
	}
	return validateCargoGroupKnownVersions(groupID, owners, append(indexes, additional...))
}
