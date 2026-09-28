package repository

import (
	"context"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/protocol/cargo"
)

func cargoProxyNameKey(repositoryID, name string) (string, error) {
	identity, err := cargo.NormalizeIdentity(name, "0.0.0")
	if err != nil || repositoryID == "" {
		return "", ErrInvalidCargoIdentity
	}
	return repositoryID + "\x00" + identity.CollisionKey, nil
}

func cargoProxyCrateKey(repositoryID, name, version string) (string, error) {
	identity, err := cargo.NormalizeIdentity(name, version)
	if err != nil || repositoryID == "" {
		return "", ErrInvalidCargoIdentity
	}
	return repositoryID + "\x00" + identity.CollisionKey + "\x00" + identity.VersionKey, nil
}

func (s *MemoryStore) GetCargoProxyConfig(ctx context.Context, repositoryID string) (CargoProxyConfig, error) {
	if err := ctx.Err(); err != nil {
		return CargoProxyConfig{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.cargoProxyConfigs[repositoryID]
	if !ok {
		return CargoProxyConfig{}, ErrNotFound
	}
	return value, nil
}

func (s *MemoryStore) PutCargoProxyConfig(ctx context.Context, value CargoProxyConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if value.RepositoryID == "" || value.DownloadTemplate == "" || value.FetchedAt.IsZero() || !value.ExpiresAt.After(value.FetchedAt) {
		return ErrInvalidCargoIdentity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[value.RepositoryID]
	if !ok || repo.Format != FormatCargo || repo.Type != RepositoryTypeProxy || repo.State != RepositoryActive {
		return ErrNotFound
	}
	if s.cargoProxyConfigs == nil {
		s.cargoProxyConfigs = make(map[string]CargoProxyConfig)
	}
	s.cargoProxyConfigs[value.RepositoryID] = value
	return nil
}

func (s *MemoryStore) GetCargoProxyIndex(ctx context.Context, repositoryID, name string) (CargoProxyIndex, error) {
	if err := ctx.Err(); err != nil {
		return CargoProxyIndex{}, err
	}
	key, err := cargoProxyNameKey(repositoryID, name)
	if err != nil {
		return CargoProxyIndex{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.cargoProxyIndexes[key]
	if !ok {
		return CargoProxyIndex{}, ErrNotFound
	}
	value.Body = append([]byte(nil), value.Body...)
	return value, nil
}

func (s *MemoryStore) PutCargoProxyIndex(ctx context.Context, value CargoProxyIndex) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, err := cargoProxyNameKey(value.RepositoryID, value.Name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[value.RepositoryID]
	if !ok || repo.Format != FormatCargo || repo.Type != RepositoryTypeProxy || repo.State != RepositoryActive {
		return ErrNotFound
	}
	var previous *CargoProxyIndex
	if current, found := s.cargoProxyIndexes[key]; found {
		if !strings.EqualFold(current.Name, value.Name) {
			return ErrUpstreamChanged
		}
		previous = &current
	}
	if err := validateCargoProxyIndex(value, previous); err != nil {
		return err
	}
	if value.Status == 200 {
		for crateKey, cached := range s.cargoProxyCrates {
			if !strings.HasPrefix(crateKey, key+"\x00") {
				continue
			}
			checksum, err := cargoProxyChecksum(value, cached.Version)
			if err != nil || checksum != cached.Checksum {
				return ErrUpstreamChanged
			}
		}
	}
	if s.cargoProxyIndexes == nil {
		s.cargoProxyIndexes = make(map[string]CargoProxyIndex)
	}
	value.Body = append([]byte(nil), value.Body...)
	s.cargoProxyIndexes[key] = value
	return nil
}

func (s *MemoryStore) GetCargoProxyCrate(ctx context.Context, repositoryID, name, version string) (CargoProxyCrate, error) {
	if err := ctx.Err(); err != nil {
		return CargoProxyCrate{}, err
	}
	key, err := cargoProxyCrateKey(repositoryID, name, version)
	if err != nil {
		return CargoProxyCrate{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.cargoProxyCrates[key]
	if !ok {
		return CargoProxyCrate{}, ErrNotFound
	}
	return value, nil
}

func (s *MemoryStore) PutCargoProxyCrate(ctx context.Context, value CargoProxyCrate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	nameKey, err := cargoProxyNameKey(value.RepositoryID, value.Name)
	if err != nil {
		return err
	}
	crateKey, err := cargoProxyCrateKey(value.RepositoryID, value.Name, value.Version)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, ok := s.hostedRepositories[value.RepositoryID]
	if !ok || repo.Format != FormatCargo || repo.Type != RepositoryTypeProxy || repo.State != RepositoryActive {
		return ErrNotFound
	}
	index, found := s.cargoProxyIndexes[nameKey]
	if !found {
		return ErrNotFound
	}
	if err := validateCargoProxyCrate(value, index); err != nil {
		return err
	}
	if previous, found := s.cargoProxyCrates[crateKey]; found && (previous.Checksum != value.Checksum || previous.Size != value.Size) {
		return ErrUpstreamChanged
	}
	if s.cargoProxyCrates == nil {
		s.cargoProxyCrates = make(map[string]CargoProxyCrate)
	}
	s.cargoProxyCrates[crateKey] = value
	return nil
}
