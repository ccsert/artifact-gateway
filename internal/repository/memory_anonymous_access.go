package repository

import "context"

func defaultAnonymousAccessPolicy() AnonymousAccessPolicy {
	return AnonymousAccessPolicy{Version: "1"}
}

func (s *MemoryStore) GetAnonymousAccessPolicy(_ context.Context) (AnonymousAccessPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.anonymousAccessPolicy.Version == "" {
		return defaultAnonymousAccessPolicy(), nil
	}
	return s.anonymousAccessPolicy, nil
}

func (s *MemoryStore) ReplaceAnonymousAccessPolicy(_ context.Context, policy AnonymousAccessPolicy, expectedVersion string) (AnonymousAccessPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.anonymousAccessPolicy
	if current.Version == "" {
		current = defaultAnonymousAccessPolicy()
	}
	if current.Version != expectedVersion {
		return AnonymousAccessPolicy{}, ErrVersionConflict
	}
	policy.Version = nextHostedGroupVersion(current.Version)
	s.anonymousAccessPolicy = policy
	return policy, nil
}
