package authorization

import (
	"context"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type grantStoreStub struct {
	set repository.RepositoryGrantSet
	err error
}

func (s grantStoreStub) GetRepositoryGrants(context.Context, string) (repository.RepositoryGrantSet, error) {
	return s.set, s.err
}

func (grantStoreStub) ReplaceRepositoryGrants(context.Context, string, []repository.RepositoryGrant, string) (repository.RepositoryGrantSet, error) {
	panic("unexpected ReplaceRepositoryGrants call")
}

func TestAuthenticateBasicReturnsConfiguredActorPrincipal(t *testing.T) {
	authenticator := Authenticator{
		ResolverToken:     "resolver-secret",
		RepositoryReaders: map[string][]string{"ci": {"team/*"}},
	}

	principal, ok := authenticator.AuthenticateBasic("ci", "resolver-secret")
	if !ok || principal.Actor != "ci" || !authenticator.CanReadRepository(principal, "team/app") {
		t.Fatalf("principal=%+v authenticated=%t", principal, ok)
	}
	if _, ok := authenticator.AuthenticateBasic("ci", "wrong-secret"); ok {
		t.Fatal("incorrect resolver credential was accepted")
	}
}

func TestRepositoryAuthorizerManagedGrantSetOverridesLegacyPolicy(t *testing.T) {
	authorizer := RepositoryAuthorizer{
		Grants: grantStoreStub{set: repository.RepositoryGrantSet{Version: "2"}},
		Legacy: Authenticator{RepositoryReaders: map[string][]string{"reader": {"releases"}}},
	}
	target := repository.HostedRepository{ID: "repo-id", Name: "releases"}

	decision := authorizer.Authorize(context.Background(), Principal{Actor: "reader"}, target, RepositoryRead)
	if decision.Allowed || decision.Source != "repository_grants" || decision.Reason != "scope_not_granted" {
		t.Fatalf("managed decision=%+v", decision)
	}
}
