package repository

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *MemoryStore) CreateServiceAccount(_ context.Context, account ServiceAccount) (ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.serviceAccounts {
		if strings.EqualFold(existing.Name, account.Name) {
			return ServiceAccount{}, ErrNameExists
		}
	}
	now := time.Now().UTC()
	account.State = ServiceAccountActive
	account.CreatedAt = now
	account.UpdatedAt = now
	account.Version = "1"
	s.serviceAccounts[account.ID] = account
	return account, nil
}

func (s *MemoryStore) CreateServiceAccountCredential(_ context.Context, credential APIKey) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.serviceAccounts[credential.ServiceAccountID]
	if !ok {
		return APIKey{}, ErrNotFound
	}
	if account.State != ServiceAccountActive {
		return APIKey{}, ErrServiceAccountDisabled
	}
	if _, exists := s.apiKeys[credential.ID]; exists {
		return APIKey{}, ErrNameExists
	}
	credential.CreatedAt = time.Now().UTC()
	credential.Roles = append([]string(nil), credential.Roles...)
	s.apiKeys[credential.ID] = credential
	return credential, nil
}

func (s *MemoryStore) ListServiceAccounts(_ context.Context, limit int, afterID string) ([]ServiceAccount, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	accounts := make([]ServiceAccount, 0, len(s.serviceAccounts))
	for _, account := range s.serviceAccounts {
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	start := sort.Search(len(accounts), func(i int) bool { return accounts[i].ID > afterID })
	accounts = accounts[start:]
	if len(accounts) > limit {
		accounts = accounts[:limit]
	}
	return accounts, nil
}

func (s *MemoryStore) GetServiceAccount(_ context.Context, id string) (ServiceAccount, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	account, ok := s.serviceAccounts[id]
	if !ok {
		return ServiceAccount{}, ErrNotFound
	}
	return account, nil
}

func (s *MemoryStore) UpdateServiceAccount(_ context.Context, update ServiceAccountUpdate, expectedVersion string) (ServiceAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	account, ok := s.serviceAccounts[update.ID]
	if !ok {
		return ServiceAccount{}, ErrNotFound
	}
	if account.Version != expectedVersion {
		return ServiceAccount{}, ErrVersionConflict
	}
	if update.Description != nil {
		account.Description = *update.Description
	}
	if update.State != nil {
		account.State = *update.State
	}
	version, err := strconv.Atoi(account.Version)
	if err != nil {
		return ServiceAccount{}, err
	}
	account.Version = strconv.Itoa(version + 1)
	account.UpdatedAt = time.Now().UTC()
	s.serviceAccounts[account.ID] = account
	return account, nil
}

func (s *MemoryStore) ListServiceAccountCredentials(_ context.Context, accountID string, limit int, afterID string) ([]APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.serviceAccounts[accountID]; !ok {
		return nil, ErrNotFound
	}
	credentials := make([]APIKey, 0)
	for _, key := range s.apiKeys {
		if key.ServiceAccountID == accountID {
			key.Roles = append([]string(nil), key.Roles...)
			credentials = append(credentials, key)
		}
	}
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].ID < credentials[j].ID })
	start := sort.Search(len(credentials), func(i int) bool { return credentials[i].ID > afterID })
	credentials = credentials[start:]
	if len(credentials) > limit {
		credentials = credentials[:limit]
	}
	return credentials, nil
}

func (s *MemoryStore) RevokeServiceAccountCredential(_ context.Context, accountID, credentialID string) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.apiKeys[credentialID]
	if !ok || key.ServiceAccountID != accountID {
		return APIKey{}, ErrNotFound
	}
	if key.RevokedAt == nil {
		now := time.Now().UTC()
		key.RevokedAt = &now
		s.apiKeys[credentialID] = key
	}
	return key, nil
}
