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
	RepositoryRead         RepositoryOperation = "read"
	RepositoryWrite        RepositoryOperation = "write"
	RepositoryAdmin        RepositoryOperation = "admin"
	RepositoryIntelligence RepositoryOperation = "intelligence"
)

// Role is a coarse, globally-scoped capability granted to a credential such as
// an API key or OIDC identity. It is evaluated before per-repository grants so
// an administrator can issue a bounded credential without enumerating every
// repository. The empty value means no role-derived capability, which keeps
// existing static-token and grant behavior unchanged. RoleMember means the same
// thing for a named account: no role-derived capability at all, so its
// repository authority comes only from per-repository grants and assigning it
// never implies access to every repository.
//
// The removed legacy values "reader" and "writer" are deliberately not
// recognized any more: no constant names them, RoleAllows never grants for
// them, and roleRank ranks them below every recognized role, so a stale value
// that survived in a credential, a session claim, or a database row authorizes
// nothing instead of reaching every repository.
type Role string

const (
	RoleNone   Role = "none"
	RoleMember Role = "member"
	RoleAdmin  Role = "admin"
)

// RoleAllows reports whether a role grants the operation. Only admin grants
// anything globally. Member grants nothing here on purpose: per-repository
// grants decide, which is what stops a single global role from covering every
// repository. Every other value, including the removed reader and writer roles,
// grants nothing.
func RoleAllows(role Role, _ RepositoryOperation) bool {
	return role == RoleAdmin
}

// RoleFromRoles picks the most privileged recognized role from a credential's
// role list. Unrecognized roles are ignored, so a list holding only removed
// legacy values selects no role at all.
func RoleFromRoles(roles []string) Role {
	best := Role("")
	for _, r := range roles {
		candidate := Role(r)
		if roleRank(candidate) > roleRank(best) {
			best = candidate
		}
	}
	return best
}

// roleRank orders recognized roles by capability. An unrecognized role ranks
// below every recognized one, so it is never selected.
func roleRank(role Role) int {
	switch role {
	case RoleAdmin:
		return 2
	case RoleMember:
		return 1
	}
	return 0
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
	if decision, blocked := accountStateDecision(principal); blocked {
		return decision
	}
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

// accountStateDecision reports a principal-wide block that must be evaluated
// before any repository authority, so every repository path - management,
// browse, search, protocol, and Group members - agrees on it.
func accountStateDecision(principal Principal) (AuthorizationDecision, bool) {
	reason := principal.AccountStateReason()
	if reason == "" {
		return AuthorizationDecision{}, false
	}
	return AuthorizationDecision{Source: "role", Reason: reason}, true
}

// ManagedDecision evaluates the per-principal grant entries for a target and
// reports whether they decided. Callers serving legacy Groups use this to
// distinguish an unbound member from an explicit grant denial without weakening
// their existing static policy.
func (a RepositoryAuthorizer) ManagedDecision(ctx context.Context, principal Principal, target repository.HostedRepository, operation RepositoryOperation) (AuthorizationDecision, bool) {
	return a.ManagedResourceDecision(ctx, principal, target, operation, "")
}

func (a RepositoryAuthorizer) ManagedResourceDecision(ctx context.Context, principal Principal, target repository.HostedRepository, operation RepositoryOperation, resource string) (AuthorizationDecision, bool) {
	if decision, blocked := accountStateDecision(principal); blocked {
		return decision, true
	}
	if principal.Admin {
		return AuthorizationDecision{Allowed: true, Source: "administrator", Reason: "administrator"}, true
	}
	if RoleAllows(principal.Role, operation) {
		return AuthorizationDecision{Allowed: true, Source: "role", Reason: "role_" + string(principal.Role)}, true
	}
	if a.Grants == nil {
		return AuthorizationDecision{}, false
	}
	set, err := a.Grants.GetRepositoryGrants(ctx, target.ID)
	if err != nil {
		return AuthorizationDecision{Source: "repository_grants", Reason: "grant_lookup_failed"}, true
	}
	managed := isManagedRepositoryGrantSet(set.Version)
	attributed := false
	for _, grant := range set.Grants {
		if grant.Principal != principal.Actor {
			continue
		}
		attributed = true
		if grantAllows(grant.Scopes, operation) && grantMatchesResource(grant.ResourcePrefix, resource) {
			return AuthorizationDecision{Allowed: true, Source: "repository_grants", Reason: "scope_granted"}, true
		}
	}
	// A grant set that names this principal decides for it, so an explicit
	// per-principal entry never needs the set to be marked managed. Anything
	// else in an unmanaged set stays invisible and legacy static policy keeps
	// deciding, which is what lets grants be materialized without switching a
	// repository away from its legacy readers.
	if !managed && !attributed {
		return AuthorizationDecision{}, false
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
		case RepositoryIntelligence:
			if scope == "repositories:intelligence" || scope == "repositories:admin" {
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
