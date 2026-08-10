package repository

import "context"

func (s *MemoryStore) GetRepositorySecurityPolicy(_ context.Context, repositoryID string) (RepositorySecurityPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.hostedRepositories[repositoryID]; !ok {
		return RepositorySecurityPolicy{}, ErrNotFound
	}
	policy, ok := s.securityPolicies[repositoryID]
	if !ok {
		return DefaultRepositorySecurityPolicy(), nil
	}
	return CloneRepositorySecurityPolicy(policy), nil
}

func (s *MemoryStore) ReplaceRepositorySecurityPolicy(_ context.Context, repositoryID string, policy RepositorySecurityPolicy, expectedVersion string) (RepositorySecurityPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hostedRepositories[repositoryID]; !ok {
		return RepositorySecurityPolicy{}, ErrNotFound
	}
	current, ok := s.securityPolicies[repositoryID]
	if !ok {
		current = DefaultRepositorySecurityPolicy()
	}
	if current.Version != expectedVersion {
		return RepositorySecurityPolicy{}, ErrVersionConflict
	}
	if policy.MaxAllowedSeverity == "" {
		policy.MaxAllowedSeverity = current.MaxAllowedSeverity
	}
	if policy.AllowedLicenses == nil {
		policy.AllowedLicenses = []string{}
	}
	policy.Version = nextHostedGroupVersion(current.Version)
	policy = CloneRepositorySecurityPolicy(policy)
	s.securityPolicies[repositoryID] = policy
	return CloneRepositorySecurityPolicy(policy), nil
}
