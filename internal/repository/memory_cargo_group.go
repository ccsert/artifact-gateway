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
