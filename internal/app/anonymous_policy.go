package app

import (
	"context"
	"net/http"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const anonymousActor = "anonymous"

const (
	anonymousAuthorizationSource = "anonymous_policy"
	anonymousAuthorizationReason = "global_and_resource_policy_enabled"
)

func anonymousPrincipal() Principal {
	return Principal{Actor: anonymousActor}
}

func isAnonymous(principal Principal) bool {
	return principal.Actor == anonymousActor
}

func anonymousReadMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func anonymousAccessAllowed(ctx context.Context, source any) bool {
	store, ok := source.(repository.AnonymousAccessPolicyStore)
	if !ok {
		return false
	}
	policy, err := store.GetAnonymousAccessPolicy(ctx)
	return err == nil && policy.Enabled
}

func anonymousHostedRepositoryReadAllowed(ctx context.Context, source any, repo repository.HostedRepository, method string) bool {
	return anonymousReadMethod(method) && repo.AnonymousRead && anonymousAccessAllowed(ctx, source)
}

func anonymousHostedGroupReadAllowed(ctx context.Context, policySource any, repositories repository.HostedRepositoryStore, group repository.HostedGroup, method string) bool {
	if !anonymousReadMethod(method) || !group.AnonymousRead || !anonymousAccessAllowed(ctx, policySource) {
		return false
	}
	for _, member := range group.Members {
		repo, err := repositories.GetHostedRepository(ctx, member.RepositoryID)
		if err == nil && repo.State == repository.RepositoryActive && repo.Format == group.Format && repo.AnonymousRead {
			return true
		}
	}
	return false
}

func anonymousHostedGroupMembers(group repository.HostedGroup, members []repository.Member) []repository.Member {
	if !group.AnonymousRead {
		return nil
	}
	allowed := make([]repository.Member, 0, len(members))
	for _, member := range members {
		if member.Anonymous {
			allowed = append(allowed, member)
		}
	}
	return allowed
}

func (h OCIHandler) anonymousOCIAllowed(ctx context.Context, groupName string) bool {
	if h.Resolver.Store == nil || !anonymousAccessAllowed(ctx, h.Resolver.Store) {
		return false
	}
	group, err := h.Resolver.Store.GetGroup(ctx, groupName)
	if err != nil || !group.Enabled || !group.Anonymous {
		return false
	}
	for _, member := range group.Members {
		if member.Anonymous {
			return true
		}
	}
	return false
}

func (h MavenHandler) anonymousMavenAllowed(ctx context.Context, groupName string) bool {
	if h.Store == nil || !anonymousAccessAllowed(ctx, h.Store) {
		return false
	}
	group, err := h.Store.GetMavenGroup(ctx, groupName)
	if err != nil || !group.Enabled || !group.Anonymous {
		return false
	}
	for _, member := range group.Members {
		if member.Anonymous {
			return true
		}
	}
	return false
}

func (h ConanHandler) anonymousConanAllowed(ctx context.Context, groupName string) bool {
	if h.Store == nil || !anonymousAccessAllowed(ctx, h.Store) {
		return false
	}
	group, err := h.Store.GetConanGroup(ctx, groupName)
	if err != nil || !group.Enabled || !group.Anonymous {
		return false
	}
	for _, member := range group.Members {
		if member.Anonymous {
			return true
		}
	}
	return false
}
