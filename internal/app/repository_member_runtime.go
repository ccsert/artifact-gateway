package app

import (
	"context"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type groupMemberAccess struct {
	Repositories repository.HostedRepositoryStore
	Authorizer   RepositoryAuthorizer
	Format       repository.Format
}

func (a groupMemberAccess) managedDecision(ctx context.Context, principal Principal, member repository.Member, resource string) (AuthorizationDecision, bool) {
	return ManagedGroupMemberDecision(ctx, a.Repositories, a.Authorizer, principal, member, a.Format, resource)
}

func (a groupMemberAccess) filterManaged(ctx context.Context, principal Principal, members []repository.Member, resource string, deny func(repository.Member, AuthorizationDecision) error) ([]repository.Member, bool, error) {
	eligible := make([]repository.Member, 0, len(members))
	hadDenied := false
	for _, member := range members {
		decision, managed := a.managedDecision(ctx, principal, member, resource)
		if managed && !decision.Allowed {
			if deny != nil {
				if err := deny(member, decision); err != nil {
					return nil, false, err
				}
			}
			hadDenied = true
			continue
		}
		eligible = append(eligible, member)
	}
	return eligible, hadDenied, nil
}

func cacheSourceMember(memberName, endpoint string, members []repository.Member) (repository.Member, bool) {
	for _, member := range members {
		if member.Type == repository.MemberProxy && member.Name == memberName && member.Endpoint == endpoint {
			return member, true
		}
	}
	return repository.Member{}, false
}

func cacheSourcePresent(memberName, endpoint string, members []repository.Member) bool {
	_, ok := cacheSourceMember(memberName, endpoint, members)
	return ok
}

func cacheSourceAllowed(memberName, endpoint string, members []repository.Member, proxyAllowed func(string) bool) bool {
	member, ok := cacheSourceMember(memberName, endpoint, members)
	return ok && proxyAllowed != nil && proxyAllowed(member.Endpoint)
}
