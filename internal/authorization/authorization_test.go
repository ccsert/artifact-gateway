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

func TestRoleAllowsGrantsBoundedOperations(t *testing.T) {
	for _, tc := range []struct {
		role Role
		op   RepositoryOperation
		want bool
	}{
		{RoleReader, RepositoryRead, true},
		{RoleReader, RepositoryWrite, false},
		{RoleReader, RepositoryAdmin, false},
		{RoleWriter, RepositoryRead, true},
		{RoleWriter, RepositoryWrite, true},
		{RoleWriter, RepositoryAdmin, false},
		{RoleAdmin, RepositoryRead, true},
		{RoleAdmin, RepositoryAdmin, true},
		{"", RepositoryRead, false},
	} {
		if got := RoleAllows(tc.role, tc.op); got != tc.want {
			t.Errorf("RoleAllows(%q,%q)=%v want=%v", tc.role, tc.op, got, tc.want)
		}
	}
}

func TestRoleFromRolesPicksMostPrivileged(t *testing.T) {
	if got := RoleFromRoles([]string{"reader", "writer"}); got != RoleWriter {
		t.Fatalf("reader+writer=%q want writer", got)
	}
	if got := RoleFromRoles([]string{"writer", "admin"}); got != RoleAdmin {
		t.Fatalf("writer+admin=%q want admin", got)
	}
	if got := RoleFromRoles([]string{"unknown", "reader"}); got != RoleReader {
		t.Fatalf("unknown+reader=%q want reader", got)
	}
	if got := RoleFromRoles([]string{"unknown"}); got != Role("") {
		t.Fatalf("unknown=%q want empty", got)
	}
}

func TestRepositoryAuthorizerHonorsPrincipalRoleBeforeGrants(t *testing.T) {
	// A managed grant set that denies the actor, plus a configured legacy
	// policy, so the only path to "allowed" is the role check.
	authorizer := RepositoryAuthorizer{
		Grants: grantStoreStub{set: repository.RepositoryGrantSet{Version: "2"}},
		Legacy: Authenticator{RepositoryReaders: map[string][]string{}},
	}
	target := repository.HostedRepository{ID: "repo-id", Name: "releases"}

	reader := Principal{Actor: "k", Role: RoleReader}
	if d := authorizer.Authorize(context.Background(), reader, target, RepositoryRead); !d.Allowed || d.Source != "role" {
		t.Fatalf("reader read=%+v", d)
	}
	if d := authorizer.Authorize(context.Background(), reader, target, RepositoryWrite); d.Allowed {
		t.Fatalf("reader write allowed=%+v", d)
	}

	writer := Principal{Actor: "k", Role: RoleWriter}
	if d := authorizer.Authorize(context.Background(), writer, target, RepositoryWrite); !d.Allowed {
		t.Fatalf("writer write=%+v", d)
	}
	if d := authorizer.Authorize(context.Background(), writer, target, RepositoryAdmin); d.Allowed {
		t.Fatalf("writer admin allowed=%+v", d)
	}

	if d := authorizer.Authorize(context.Background(), Principal{Actor: "k"}, target, RepositoryRead); d.Allowed {
		t.Fatalf("empty role allowed read=%+v", d)
	}
}

func TestAuthenticatorMapsAPIKeyRolesToPrincipal(t *testing.T) {
	store := repository.NewMemoryStore()
	authenticator := Authenticator{APIKeys: store}
	makeKey := func(t *testing.T, roles []string) (string, string) {
		t.Helper()
		token := "agk_" + roles[0]
		key, err := store.CreateAPIKey(context.Background(), repository.APIKey{ID: uuid.NewString(), Name: roles[0], SecretHash: HashAPIKey(token), Roles: roles})
		if err != nil {
			t.Fatal(err)
		}
		return token, key.ID
	}

	readerToken, readerID := makeKey(t, []string{"reader"})
	rp, rok := authenticator.Authenticate("Bearer " + readerToken)
	if !rok || rp.Role != RoleReader || rp.Admin || rp.Actor != "api-key:"+readerID {
		t.Fatalf("reader principal=%#v ok=%t", rp, rok)
	}
	writerToken, writerID := makeKey(t, []string{"writer"})
	wp, wok := authenticator.Authenticate("Bearer " + writerToken)
	if !wok || wp.Role != RoleWriter || wp.Admin || wp.Actor != "api-key:"+writerID {
		t.Fatalf("writer principal=%#v ok=%t", wp, wok)
	}
	adminToken, adminID := makeKey(t, []string{"admin"})
	ap, aok := authenticator.Authenticate("Bearer " + adminToken)
	if !aok || ap.Role != RoleAdmin || !ap.Admin || ap.Actor != "api-key:"+adminID {
		t.Fatalf("admin principal=%#v ok=%t", ap, aok)
	}

	// A reader credential can read (role) but cannot write, with a configured policy.
	authenticator.RepositoryReaders = map[string][]string{}
	authenticator.RepositoryWriters = map[string][]string{}
	if !authenticator.CanReadRepository(rp, "any") {
		t.Fatal("reader denied read")
	}
	if authenticator.CanWriteMavenRepository(rp, "any") {
		t.Fatal("reader allowed write")
	}
}
