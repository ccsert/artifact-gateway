package repository

import (
	"context"
	"errors"
)

func DefaultRepositoryQuarantineReadPolicy() RepositoryQuarantineReadPolicy {
	return RepositoryQuarantineReadPolicy{Version: "1", Enabled: false}
}

func (s *MemoryStore) GetRepositoryQuarantineReadPolicy(_ context.Context, repositoryID string) (RepositoryQuarantineReadPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.hostedRepositories[repositoryID]; !ok {
		return RepositoryQuarantineReadPolicy{}, ErrNotFound
	}
	if policy, ok := s.quarantineReadPolicies[repositoryID]; ok {
		return policy, nil
	}
	return DefaultRepositoryQuarantineReadPolicy(), nil
}

func (s *MemoryStore) ReplaceRepositoryQuarantineReadPolicy(_ context.Context, repositoryID string, policy RepositoryQuarantineReadPolicy, expectedVersion string) (RepositoryQuarantineReadPolicy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hostedRepositories[repositoryID]; !ok {
		return RepositoryQuarantineReadPolicy{}, ErrNotFound
	}
	current, ok := s.quarantineReadPolicies[repositoryID]
	if !ok {
		current = DefaultRepositoryQuarantineReadPolicy()
	}
	if current.Version != expectedVersion {
		return RepositoryQuarantineReadPolicy{}, ErrVersionConflict
	}
	policy.Version = nextHostedGroupVersion(current.Version)
	s.quarantineReadPolicies[repositoryID] = policy
	return policy, nil
}

// QuarantinedArtifactReadBlocked evaluates the repository-local read policy
// against one atomic artifact identity. Missing quarantine state is allowed.
func QuarantinedArtifactReadBlocked(ctx context.Context, policies RepositoryQuarantineReadPolicyStore, quarantines ArtifactQuarantineStore, repositoryID string, format Format, coordinate string, digests ...string) (bool, error) {
	policy, err := policies.GetRepositoryQuarantineReadPolicy(ctx, repositoryID)
	if err != nil {
		return false, err
	}
	if !policy.Enabled {
		return false, nil
	}
	for _, digest := range digests {
		value, quarantineErr := quarantines.GetArtifactQuarantine(ctx, repositoryID, format, coordinate, digest)
		if errors.Is(quarantineErr, ErrNotFound) {
			continue
		}
		if quarantineErr != nil {
			return false, quarantineErr
		}
		if value.State == ArtifactQuarantineStateQuarantined {
			return true, nil
		}
	}
	return false, nil
}
