package authorization

import (
	"context"
	"strconv"
	"strings"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// RepositoryOperation is the protocol-independent action being authorized.
type RepositoryOperation string

const (
	RepositoryRead  RepositoryOperation = "read"
	RepositoryWrite RepositoryOperation = "write"
	RepositoryAdmin RepositoryOperation = "admin"
)

// Role is a coarse, globally-scoped capability granted to a credential such as
// an API key or OIDC identity. It is evaluated before per-repository grants so
// an administrator can issue a bounded credential without enumerating every
// repository. The empty value means no role-derived capability, which keeps
// existing static-token and grant behavior unchanged.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleWriter Role = "writer"
	RoleReader Role = "reader"
)

// RoleAllows reports whether a role grants the operation. Admin grants all,
// writer grants read and write, reader grants read only.
func RoleAllows(role Role, operation RepositoryOperation) bool {
	switch role {
	case RoleAdmin:
		return true
	case RoleWriter:
		return operation == RepositoryRead || operation == RepositoryWrite
	case RoleReader:
		return operation == RepositoryRead
	}
	return false
}

// RoleFromRoles picks the most privileged recognized role from a credential's
// role list. Unrecognized roles are ignored.
func RoleFromRoles(roles []string) Role {
	best := Role("")
	for _, r := range roles {
		switch Role(r) {
		case RoleAdmin:
			return RoleAdmin
		case RoleWriter:
			if best != RoleAdmin {
				best = RoleWriter
			}
		case RoleReader:
			if best == "" {
				best = RoleReader
			}
		}
	}
	return best
}

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
	return a.AuthorizeResource(ctx, principal, target, operation, "")
}

func (a RepositoryAuthorizer) AuthorizeResource(ctx context.Context, principal Principal, target repository.HostedRepository, operation RepositoryOperation, resource string) AuthorizationDecision {
	if principal.Admin {
		return AuthorizationDecision{Allowed: true, Source: "administrator", Reason: "administrator"}
	}
	if RoleAllows(principal.Role, operation) {
		return AuthorizationDecision{Allowed: true, Source: "role", Reason: "role_" + string(principal.Role)}
	}
	if decision, managed := a.ManagedResourceDecision(ctx, principal, target, operation, resource); managed {
		return decision
	}
	return a.authorizeLegacyForTarget(principal, target, operation)
}

// ManagedDecision evaluates only an explicitly managed grant set. Callers
// serving legacy Groups use this to distinguish an unbound member from an
// explicit grant denial without weakening their existing static policy.
func (a RepositoryAuthorizer) ManagedDecision(ctx context.Context, principal Principal, target repository.HostedRepository, operation RepositoryOperation) (AuthorizationDecision, bool) {
	return a.ManagedResourceDecision(ctx, principal, target, operation, "")
}

func (a RepositoryAuthorizer) ManagedResourceDecision(ctx context.Context, principal Principal, target repository.HostedRepository, operation RepositoryOperation, resource string) (AuthorizationDecision, bool) {
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
		if grant.Principal == principal.Actor && grantAllows(grant.Scopes, operation) && grantMatchesResource(grant.ResourcePrefix, resource) {
			return AuthorizationDecision{Allowed: true, Source: "repository_grants", Reason: "scope_granted"}, true
		}
	}
	return AuthorizationDecision{Source: "repository_grants", Reason: "scope_not_granted"}, true
}

func grantMatchesResource(prefix, resource string) bool {
	if prefix == "" {
		return true
	}
	return resource != "" && strings.HasPrefix(resource, prefix)
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
func ManagedGroupMemberDecision(ctx context.Context, repositories repository.HostedRepositoryStore, authorizer RepositoryAuthorizer, principal Principal, member repository.Member, format repository.Format, resource string) (AuthorizationDecision, bool) {
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
	return authorizer.ManagedResourceDecision(ctx, principal, target, RepositoryRead, resource)
}
