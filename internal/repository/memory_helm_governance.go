package repository

import "context"

func (s *MemoryStore) TransitionHelmGovernance(ctx context.Context, in HelmGovernanceChange) (HelmPublication, error) {
	if !validHelmGovernanceChange(in) {
		return HelmPublication{}, ErrInvalidHelmPublication
	}
	if err := ctx.Err(); err != nil {
		return HelmPublication{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, err := s.activeHelmRepositoryLocked(in.RepositoryID)
	if err != nil {
		return HelmPublication{}, err
	}
	key := helmKey(repo.ID, in.Name, in.Version)
	p, exists := s.helmPublications[key]
	if !exists {
		return HelmPublication{}, ErrNotFound
	}
	p, changed, err := transitionHelmGovernance(p, in)
	if err != nil {
		return HelmPublication{}, err
	}
	if changed {
		s.helmPublications[key] = p
		s.appendAuditLocked(withRequestContextAudit(ctx, helmGovernanceAudit(p, in, repo.Name)))
	}
	return cloneHelmPublication(p), nil
}

func (s *MemoryStore) ReplaceHelmQuarantineReadPolicy(ctx context.Context, id string, policy RepositoryQuarantineReadPolicy, expectedVersion, actor string) (RepositoryQuarantineReadPolicy, error) {
	if !sourceVersionValid(expectedVersion) || !validHelmGovernanceActor(actor) {
		return RepositoryQuarantineReadPolicy{}, ErrInvalidHelmPublication
	}
	if err := ctx.Err(); err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, err := s.activeHelmRepositoryLocked(id)
	if err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	current, exists := s.quarantineReadPolicies[id]
	if !exists {
		current = DefaultRepositoryQuarantineReadPolicy()
	}
	if current.Version != expectedVersion {
		return RepositoryQuarantineReadPolicy{}, ErrVersionConflict
	}
	if policy.Enabled == current.Enabled {
		return current, nil
	}
	policy.Version = nextHostedGroupVersion(current.Version)
	s.quarantineReadPolicies[id] = policy
	s.appendAuditLocked(withRequestContextAudit(ctx, helmReadPolicyAudit(repo, policy, actor)))
	return policy, nil
}
