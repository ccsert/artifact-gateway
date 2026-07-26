package repository

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) CreateAPIKey(_ context.Context, key APIKey) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key.CreatedAt = time.Now().UTC()
	s.apiKeys[key.ID] = key
	return key, nil
}

func (s *MemoryStore) ListAPIKeys(_ context.Context) ([]APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]APIKey, 0, len(s.apiKeys))
	for _, key := range s.apiKeys {
		key.Roles = append([]string(nil), key.Roles...)
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].CreatedAt.Equal(keys[j].CreatedAt) {
			return keys[i].ID < keys[j].ID
		}
		return keys[i].CreatedAt.Before(keys[j].CreatedAt)
	})
	return keys, nil
}

func (s *MemoryStore) FindActiveAPIKeyByHash(_ context.Context, hash string) (APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, key := range s.apiKeys {
		if key.SecretHash == hash && key.RevokedAt == nil {
			return key, nil
		}
	}
	return APIKey{}, ErrNotFound
}

func (s *MemoryStore) RevokeAPIKey(_ context.Context, id string) (APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.apiKeys[id]
	if !ok {
		return APIKey{}, ErrNotFound
	}
	if key.RevokedAt == nil {
		now := time.Now().UTC()
		key.RevokedAt = &now
		s.apiKeys[id] = key
	}
	return key, nil
}
