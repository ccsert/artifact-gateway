package repository

import (
	"context"
	"time"
)

func (s *MemoryStore) TransitionNuGetGovernance(ctx context.Context, in NuGetGovernanceChange) (NuGetPublication, error) {
	if !validNuGetGovernanceChange(in) {
		return NuGetPublication{}, ErrInvalidNuGetPublication
	}
	if err := ctx.Err(); err != nil {
		return NuGetPublication{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, err := s.activeNuGetRepositoryLocked(in.RepositoryID)
	if err != nil {
		return NuGetPublication{}, err
	}
	if repo.Type != RepositoryTypeHosted && (in.Action == "unlist" || in.Action == "relist") {
		return NuGetPublication{}, ErrInvalidNuGetPublication
	}
	binding := ""
	if repo.Type == RepositoryTypeProxy {
		binding = repo.Version
	}
	key := nuGetPublicationKey(repo.ID, in.PackageID, in.Version, binding)
	p, found := s.nugetPublications[key]
	if !found {
		return NuGetPublication{}, ErrNotFound
	}
	p, changed, err := transitionNuGetGovernance(p, in)
	if err != nil {
		return NuGetPublication{}, err
	}
	if changed {
		s.nugetPublications[key] = p
		s.appendAuditLocked(withRequestContextAudit(ctx, nuGetGovernanceAudit(p, in, repo.Name)))
	}
	return cloneNuGetPublication(p), nil
}

func (s *MemoryStore) ReplaceNuGetQuarantineReadPolicy(ctx context.Context, id string, policy RepositoryQuarantineReadPolicy, expectedVersion, actor string) (RepositoryQuarantineReadPolicy, error) {
	if err := ctx.Err(); err != nil {
		return RepositoryQuarantineReadPolicy{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, err := s.activeNuGetRepositoryLocked(id)
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
	policy.Version = nextHostedGroupVersion(current.Version)
	s.quarantineReadPolicies[id] = policy
	s.appendAuditLocked(withRequestContextAudit(ctx, nuGetReadPolicyAudit(repo, policy, actor)))
	return policy, nil
}

func nuGetReadPolicyAudit(repo HostedRepository, policy RepositoryQuarantineReadPolicy, actor string) AuditRecord {
	return AuditRecord{Repository: repo.Name, GroupName: repo.Name, Actor: actor, Format: string(FormatNuGet), Resource: repo.ID, Operation: "nuget.preview.quarantine_read_policy.configure", Outcome: AuditResolved, Status: 200, OccurredAt: time.Now().UTC(), Evidence: map[string]string{"version": policy.Version}}
}
