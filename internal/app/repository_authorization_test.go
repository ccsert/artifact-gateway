package app

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

func TestRepositoryAuthorizerUsesManagedGrantScopes(t *testing.T) {
	target := repository.HostedRepository{ID: "repo-id", Name: "releases"}
	authorizer := RepositoryAuthorizer{Grants: grantStoreStub{set: repository.RepositoryGrantSet{Version: "2", Grants: []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}},
		{Principal: "writer", Scopes: []string{"repositories:write"}},
		{Principal: "operator", Scopes: []string{"repositories:admin"}},
	}}}}

	cases := []struct {
		name      string
		principal string
		operation RepositoryOperation
		allowed   bool
	}{
		{name: "reader can read", principal: "reader", operation: RepositoryRead, allowed: true},
		{name: "reader cannot write", principal: "reader", operation: RepositoryWrite},
		{name: "writer inherits read", principal: "writer", operation: RepositoryRead, allowed: true},
		{name: "writer can write", principal: "writer", operation: RepositoryWrite, allowed: true},
		{name: "writer cannot administer", principal: "writer", operation: RepositoryAdmin},
		{name: "admin scope inherits write", principal: "operator", operation: RepositoryWrite, allowed: true},
		{name: "admin scope administers", principal: "operator", operation: RepositoryAdmin, allowed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := authorizer.Authorize(context.Background(), Principal{Actor: tc.principal}, target, tc.operation)
			if decision.Allowed != tc.allowed || decision.Source != "repository_grants" {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}
}

func TestRepositoryAuthorizerManagedEmptySetRevokesLegacyAccess(t *testing.T) {
	authorizer := RepositoryAuthorizer{
		Grants: grantStoreStub{set: repository.RepositoryGrantSet{Version: "2"}},
		Legacy: Authenticator{RepositoryReaders: map[string][]string{"reader": {"releases"}}},
	}
	decision := authorizer.Authorize(context.Background(), Principal{Actor: "reader"}, repository.HostedRepository{ID: "repo-id", Name: "releases"}, RepositoryRead)
	if decision.Allowed || decision.Source != "repository_grants" || decision.Reason != "scope_not_granted" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestRepositoryAuthorizerMatchesManagedGrantResourcePrefixes(t *testing.T) {
	target := repository.HostedRepository{ID: "repo-id", Name: "releases"}
	authorizer := RepositoryAuthorizer{Grants: grantStoreStub{set: repository.RepositoryGrantSet{Version: "2", Grants: []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "org.example"},
		{Principal: "writer", Scopes: []string{"repositories:write"}, ResourcePrefix: "org.example:widget"},
	}}}}

	if decision := authorizer.AuthorizeResource(context.Background(), Principal{Actor: "reader"}, target, RepositoryRead, "org.example:widget:1.0.0"); !decision.Allowed {
		t.Fatalf("reader decision=%+v", decision)
	}
	if decision := authorizer.AuthorizeResource(context.Background(), Principal{Actor: "reader"}, target, RepositoryRead, "com.other:widget:1.0.0"); decision.Allowed || decision.Reason != "scope_not_granted" {
		t.Fatalf("wrong prefix decision=%+v", decision)
	}
	if decision := authorizer.Authorize(context.Background(), Principal{Actor: "writer"}, target, RepositoryWrite); decision.Allowed || decision.Reason != "scope_not_granted" {
		t.Fatalf("repository-wide decision=%+v", decision)
	}
	if decision := authorizer.AuthorizeResource(context.Background(), Principal{Actor: "writer"}, target, RepositoryWrite, "org.example:widget:1.0.0"); !decision.Allowed {
		t.Fatalf("writer decision=%+v", decision)
	}
}

func TestRepositoryAuthorizerRetainsLegacyPolicyForDefaultGrantSet(t *testing.T) {
	authorizer := RepositoryAuthorizer{
		Grants: grantStoreStub{set: repository.RepositoryGrantSet{Version: "1"}},
		Legacy: Authenticator{
			RepositoryReaders: map[string][]string{"reader": {"team/*"}},
			RepositoryWriters: map[string][]string{"writer": {"team/releases"}},
		},
	}
	target := repository.HostedRepository{ID: "repo-id", Name: "team/releases"}
	if decision := authorizer.Authorize(context.Background(), Principal{Actor: "reader"}, target, RepositoryRead); !decision.Allowed || decision.Source != "legacy_static" {
		t.Fatalf("read decision=%+v", decision)
	}
	if decision := authorizer.Authorize(context.Background(), Principal{Actor: "writer"}, target, RepositoryWrite); !decision.Allowed || decision.Source != "legacy_static" {
		t.Fatalf("write decision=%+v", decision)
	}
}

func TestRepositoryAuthorizerFailsClosedWhenGrantLookupFails(t *testing.T) {
	authorizer := RepositoryAuthorizer{
		Grants: grantStoreStub{err: repository.ErrNotFound},
		Legacy: Authenticator{RepositoryReaders: map[string][]string{"reader": {"releases"}}},
	}
	decision := authorizer.Authorize(context.Background(), Principal{Actor: "reader"}, repository.HostedRepository{ID: "repo-id", Name: "releases"}, RepositoryRead)
	if decision.Allowed || decision.Reason != "grant_lookup_failed" {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestRepositoryAuthorizerAlwaysAllowsAdministrator(t *testing.T) {
	authorizer := RepositoryAuthorizer{Grants: grantStoreStub{err: repository.ErrNotFound}}
	decision := authorizer.Authorize(context.Background(), Principal{Actor: "admin", Admin: true}, repository.HostedRepository{ID: "repo-id", Name: "releases"}, RepositoryAdmin)
	if !decision.Allowed || decision.Source != "administrator" {
		t.Fatalf("decision=%+v", decision)
	}
}
