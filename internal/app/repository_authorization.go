package app

import (
	"context"
	"strconv"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// RepositoryOperation is the protocol-independent action being authorized.
type RepositoryOperation string

const (
	RepositoryRead  RepositoryOperation = "read"
	RepositoryWrite RepositoryOperation = "write"
	RepositoryAdmin RepositoryOperation = "admin"
)

// AuthorizationDecision explains an authorization result without tying policy
// evaluation to a protocol's HTTP error representation.
type AuthorizationDecision struct {
	Allowed bool
	Source  string
	Reason  string
}

// RepositoryAuthorizer evaluates repository grants first once an operator has
// explicitly managed a grant set. Version 1 is the store's unmodified default
// and therefore retains legacy static-policy behavior for compatibility.
type RepositoryAuthorizer struct {
	Grants         repository.RepositoryGrantStore
	Legacy         Authenticator
	LegacyFallback func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision
}

func (a RepositoryAuthorizer) Authorize(ctx context.Context, principal Principal, target repository.HostedRepository, operation RepositoryOperation) AuthorizationDecision {
	if principal.Admin {
		return AuthorizationDecision{Allowed: true, Source: "administrator", Reason: "administrator"}
	}
	if decision, managed := a.ManagedDecision(ctx, principal, target, operation); managed {
		return decision
	}
	return a.authorizeLegacyForTarget(principal, target, operation)
}

// ManagedDecision evaluates only an explicitly managed grant set. Callers
// serving legacy Groups use this to distinguish an unbound member from an
// explicit grant denial without weakening their existing static policy.
func (a RepositoryAuthorizer) ManagedDecision(ctx context.Context, principal Principal, target repository.HostedRepository, operation RepositoryOperation) (AuthorizationDecision, bool) {
	if principal.Admin {
		return AuthorizationDecision{Allowed: true, Source: "administrator", Reason: "administrator"}, true
	}
	if a.Grants == nil {
		return AuthorizationDecision{}, false
	}
	set, err := a.Grants.GetRepositoryGrants(ctx, target.ID)
	if err != nil {
		return AuthorizationDecision{Source: "repository_grants", Reason: "grant_lookup_failed"}, true
	}
	if !isManagedRepositoryGrantSet(set.Version) {
		return AuthorizationDecision{}, false
	}
	for _, grant := range set.Grants {
		if grant.Principal == principal.Actor && grantAllows(grant.Scopes, operation) {
			return AuthorizationDecision{Allowed: true, Source: "repository_grants", Reason: "scope_granted"}, true
		}
	}
	return AuthorizationDecision{Source: "repository_grants", Reason: "scope_not_granted"}, true
}

func (a RepositoryAuthorizer) authorizeLegacyForTarget(principal Principal, target repository.HostedRepository, operation RepositoryOperation) AuthorizationDecision {
	if a.LegacyFallback != nil {
		return a.LegacyFallback(principal, target, operation)
	}
	return a.authorizeLegacy(principal, target.Name, operation)
}

func (a RepositoryAuthorizer) authorizeLegacy(principal Principal, repositoryName string, operation RepositoryOperation) AuthorizationDecision {
	if principal.RepositoryPatterns == nil {
		principal.RepositoryPatterns = a.Legacy.RepositoryReaders[principal.Actor]
	}
	switch operation {
	case RepositoryRead:
		if a.Legacy.CanReadRepository(principal, repositoryName) {
			return AuthorizationDecision{Allowed: true, Source: "legacy_static", Reason: "read_pattern_granted"}
		}
	case RepositoryWrite:
		if a.Legacy.CanWriteMavenRepository(principal, repositoryName) {
			return AuthorizationDecision{Allowed: true, Source: "legacy_static", Reason: "write_pattern_granted"}
		}
	case RepositoryAdmin:
		// Non-administrators have no legacy repository-scoped admin grant.
	}
	return AuthorizationDecision{Source: "legacy_static", Reason: "scope_not_granted"}
}

func isManagedRepositoryGrantSet(version string) bool {
	value, err := strconv.ParseUint(version, 10, 64)
	return err == nil && value > 1
}

func grantAllows(scopes []string, operation RepositoryOperation) bool {
	for _, scope := range scopes {
		switch operation {
		case RepositoryRead:
			if scope == "repositories:read" || scope == "repositories:write" || scope == "repositories:admin" {
				return true
			}
		case RepositoryWrite:
			if scope == "repositories:write" || scope == "repositories:admin" {
				return true
			}
		case RepositoryAdmin:
			if scope == "repositories:admin" {
				return true
			}
		}
	}
	return false
}

// ManagedGroupMemberDecision evaluates only an explicit member-to-Repository
// binding. Empty bindings deliberately retain legacy Group behavior.
func ManagedGroupMemberDecision(ctx context.Context, repositories repository.HostedRepositoryStore, authorizer RepositoryAuthorizer, principal Principal, member repository.Member, format repository.Format) (AuthorizationDecision, bool) {
	if member.RepositoryID == "" {
		return AuthorizationDecision{}, false
	}
	if repositories == nil {
		return AuthorizationDecision{Source: "repository_grants", Reason: "grant_lookup_failed"}, true
	}
	target, err := repositories.GetHostedRepository(ctx, member.RepositoryID)
	if err != nil || target.Format != format || target.State != repository.RepositoryActive {
		return AuthorizationDecision{Source: "repository_grants", Reason: "grant_lookup_failed"}, true
	}
	return authorizer.ManagedDecision(ctx, principal, target, RepositoryRead)
}
