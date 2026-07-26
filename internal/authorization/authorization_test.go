package authorization

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type grantStoreStub struct {
	set repository.RepositoryGrantSet
	err error
}

func TestAuthenticatorAcceptsActiveAdministrativeAPIKeyAndRejectsRevokedKey(t *testing.T) {
	store := repository.NewMemoryStore()
	token := "agk_test-token"
	key, err := store.CreateAPIKey(context.Background(), repository.APIKey{ID: uuid.NewString(), Name: "automation", SecretHash: HashAPIKey(token), Roles: []string{"admin"}})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{APIKeys: store}
	principal, ok := authenticator.Authenticate("Bearer " + token)
	if !ok || !principal.Admin || principal.Actor != "api-key:"+key.ID {
		t.Fatalf("principal=%#v authenticated=%t", principal, ok)
	}
	if _, err := store.RevokeAPIKey(context.Background(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := authenticator.Authenticate("Bearer " + token); ok {
		t.Fatal("revoked API key authenticated")
	}
}

func TestHashAPIKeyDoesNotReturnPlaintext(t *testing.T) {
	token := "agk_test-token"
	digest := sha256.Sum256([]byte(token))
	if got, want := HashAPIKey(token), base64.RawURLEncoding.EncodeToString(digest[:]); got != want || got == token {
		t.Fatalf("hash=%q want=%q", got, want)
	}
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
