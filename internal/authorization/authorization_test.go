package authorization

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

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
	if !ok || !principal.Admin || principal.Actor != "api-key:"+key.ID || principal.AuthenticationKind != AuthenticationAPIKey {
		t.Fatalf("principal=%#v authenticated=%t", principal, ok)
	}
	if _, err := store.RevokeAPIKey(context.Background(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := authenticator.Authenticate("Bearer " + token); ok {
		t.Fatal("revoked API key authenticated")
	}
}

func TestAuthenticatorRejectsExpiredAPIKeyAndRecordsSuccessfulUse(t *testing.T) {
	store := repository.NewMemoryStore()
	now := time.Now().UTC()
	expiredToken := "agk_expired-token"
	_, err := store.CreateAPIKey(context.Background(), repository.APIKey{
		ID: uuid.NewString(), Name: "expired", SecretHash: HashAPIKey(expiredToken), Roles: []string{"reader"}, ExpiresAt: timePointer(now.Add(-time.Minute)),
	})
	if err != nil {
		t.Fatal(err)
	}
	activeToken := "agk_active-token"
	active, err := store.CreateAPIKey(context.Background(), repository.APIKey{
		ID: uuid.NewString(), Name: "active", SecretHash: HashAPIKey(activeToken), Roles: []string{"reader"}, ExpiresAt: timePointer(now.Add(time.Hour)),
	})
	if err != nil {
		t.Fatal(err)
	}

	authenticator := Authenticator{APIKeys: store}
	if _, ok := authenticator.Authenticate("Bearer " + expiredToken); ok {
		t.Fatal("expired API key authenticated")
	}
	if _, ok := authenticator.Authenticate("Bearer " + activeToken); !ok {
		t.Fatal("active API key did not authenticate")
	}
	keys, err := store.ListAPIKeys(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if key.ID == active.ID && key.LastUsedAt == nil {
			t.Fatal("successful authentication did not record last-used time")
		}
	}
}

func timePointer(value time.Time) *time.Time { return &value }

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
	if !ok || principal.Actor != "ci" || principal.AuthenticationKind != AuthenticationStaticResolver || !authenticator.CanReadRepository(principal, "team/app") {
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

func TestManagedResourceDecisionHonorsGlobalRole(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID: "managed-role-target", Name: "managed-role-target", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), repo.ID, nil, "1"); err != nil {
		t.Fatal(err)
	}

	authorizer := RepositoryAuthorizer{Grants: store}
	decision, managed := authorizer.ManagedResourceDecision(
		context.Background(),
		Principal{Actor: "reader", Role: RoleReader},
		repo,
		RepositoryRead,
		"release/app.txt",
	)
	if !managed || !decision.Allowed || decision.Source != "role" || decision.Reason != "role_reader" {
		t.Fatalf("managed=%v decision=%#v", managed, decision)
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

func TestAuthenticatorRestoresUserRoleForIssuedProtocolToken(t *testing.T) {
	store := repository.NewMemoryStore()
	user, err := store.CreateUser(context.Background(), repository.User{ID: "user-id", Name: "test", Role: string(RoleAdmin)})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{AdminToken: "admin-secret", ResolverToken: "resolver-secret", Users: store}
	session := authenticator.IssueUserSession(user.ID)
	principal, ok := authenticator.Authenticate("Bearer " + session)
	if !ok || !principal.Admin || principal.Role != RoleAdmin || principal.AuthenticationKind != AuthenticationLocalSession {
		t.Fatalf("session principal = %#v, ok=%v", principal, ok)
	}

	protocolToken := authenticator.IssueToken(principal.Actor)
	protocolPrincipal, ok := authenticator.Authenticate("Bearer " + protocolToken)
	if !ok || !protocolPrincipal.Admin || protocolPrincipal.Role != RoleAdmin || protocolPrincipal.AuthenticationKind != AuthenticationLocalSession {
		t.Fatalf("protocol principal = %#v, ok=%v", protocolPrincipal, ok)
	}
}

func TestAuthenticatorRestoresStaticAdminForIssuedProtocolToken(t *testing.T) {
	authenticator := Authenticator{
		AdminToken:    "admin-secret",
		ResolverToken: "resolver-secret",
		AdminActor:    "gateway-admin",
	}
	managementPrincipal, ok := authenticator.Authenticate("Bearer admin-secret")
	if !ok {
		t.Fatal("static administrator did not authenticate")
	}
	protocolToken := authenticator.IssuePrincipalToken(managementPrincipal)
	principal, ok := authenticator.Authenticate("Bearer " + protocolToken)
	if !ok || principal.Actor != "gateway-admin" || !principal.Admin || principal.Role != RoleAdmin || principal.AuthenticationKind != AuthenticationStaticAdmin {
		t.Fatalf("protocol principal = %#v, ok=%v", principal, ok)
	}
}
