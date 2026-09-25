package authorization

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"strings"
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
		ID: uuid.NewString(), Name: "expired", SecretHash: HashAPIKey(expiredToken), Roles: []string{"member"}, ExpiresAt: timePointer(now.Add(-time.Minute)),
	})
	if err != nil {
		t.Fatal(err)
	}
	activeToken := "agk_active-token"
	active, err := store.CreateAPIKey(context.Background(), repository.APIKey{
		ID: uuid.NewString(), Name: "active", SecretHash: HashAPIKey(activeToken), Roles: []string{"member"}, ExpiresAt: timePointer(now.Add(time.Hour)),
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

func TestAuthenticateBasicAcceptsServiceAccountCredentialAsStableMachinePrincipal(t *testing.T) {
	store := repository.NewMemoryStore()
	account, err := store.CreateServiceAccount(context.Background(), repository.ServiceAccount{
		ID: uuid.NewString(), Name: "maven-release-bot",
	})
	if err != nil {
		t.Fatal(err)
	}
	token := "agc_service-account-basic-token"
	if _, err := store.CreateAPIKey(context.Background(), repository.APIKey{
		ID: uuid.NewString(), ServiceAccountID: account.ID, Name: "jenkins",
		SecretHash: HashAPIKey(token), ExpiresAt: timePointer(time.Now().Add(time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{APIKeys: store, ServiceAccounts: store}

	principal, ok := authenticator.AuthenticateBasic("jenkins", token)
	if !ok || principal.Actor != "service-account:"+account.ID || principal.AuthenticationKind != AuthenticationServiceAccountCredential || principal.Admin || principal.Role != "" {
		t.Fatalf("principal=%+v authenticated=%t", principal, ok)
	}

	disabled := repository.ServiceAccountDisabled
	if _, err := store.UpdateServiceAccount(context.Background(), repository.ServiceAccountUpdate{
		ID: account.ID, State: &disabled,
	}, account.Version); err != nil {
		t.Fatal(err)
	}
	if _, ok := authenticator.AuthenticateBasic("jenkins", token); ok {
		t.Fatal("disabled Service Account credential authenticated through Basic")
	}
}

func TestAuthenticateBasicDoesNotBroadenStandaloneAPIKeyProtocolAccess(t *testing.T) {
	store := repository.NewMemoryStore()
	token := "agk_bearer-only-token"
	if _, err := store.CreateAPIKey(context.Background(), repository.APIKey{
		ID: uuid.NewString(), Name: "management-client", SecretHash: HashAPIKey(token), Roles: []string{"member"},
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{APIKeys: store, ServiceAccounts: store}
	if _, ok := authenticator.AuthenticateBasic("client", token); ok {
		t.Fatal("standalone Bearer API key unexpectedly authenticated through Basic")
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
		{RoleNone, RepositoryRead, false},
		{RoleNone, RepositoryWrite, false},
		{RoleNone, RepositoryAdmin, false},
		// member carries no repository capability of its own, and the removed
		// reader and writer roles are not recognized at all any more.
		{RoleMember, RepositoryRead, false},
		{RoleMember, RepositoryWrite, false},
		{RoleMember, RepositoryAdmin, false},
		{"reader", RepositoryRead, false},
		{"writer", RepositoryRead, false},
		{"writer", RepositoryWrite, false},
		{RoleAdmin, RepositoryRead, true},
		{RoleAdmin, RepositoryWrite, true},
		{RoleAdmin, RepositoryAdmin, true},
		{"", RepositoryRead, false},
		{"administrator", RepositoryRead, false},
	} {
		if got := RoleAllows(tc.role, tc.op); got != tc.want {
			t.Errorf("RoleAllows(%q,%q)=%v want=%v", tc.role, tc.op, got, tc.want)
		}
	}
}

func TestPendingRoleDeniesLegacyDefaultsPatternsAndManagedGrants(t *testing.T) {
	principal := Principal{Actor: "user:pending", Role: RoleNone, RepositoryPatterns: []string{"team/*"}}
	legacy := Authenticator{
		RepositoryReaders: nil,
		RepositoryWriters: map[string][]string{principal.Actor: {"team/releases"}},
	}
	if legacy.CanReadRepository(principal, "team/releases") ||
		legacy.CanReadMavenRepository(principal, "team") ||
		legacy.CanWriteMavenRepository(principal, "team/releases") {
		t.Fatal("pending account reached a legacy repository path")
	}
	target := repository.HostedRepository{ID: "repo-id", Name: "team/releases"}
	authorizer := RepositoryAuthorizer{
		Legacy: legacy,
		Grants: grantStoreStub{set: repository.RepositoryGrantSet{
			Version: "2",
			Grants: []repository.RepositoryGrant{{
				Principal: principal.Actor, Scopes: []string{"repositories:admin"},
			}},
		}},
	}
	for _, operation := range []RepositoryOperation{RepositoryRead, RepositoryWrite, RepositoryAdmin, RepositoryIntelligence} {
		decision := authorizer.Authorize(context.Background(), principal, target, operation)
		if decision.Allowed || decision.Reason != "authorization_pending" {
			t.Fatalf("operation=%s decision=%+v", operation, decision)
		}
		decision, managed := authorizer.ManagedResourceDecision(context.Background(), principal, target, operation, "")
		if !managed || decision.Allowed || decision.Reason != "authorization_pending" {
			t.Fatalf("managed operation=%s decision=%+v managed=%t", operation, decision, managed)
		}
	}
}

func TestPasswordChangeRequiredBlocksEveryRepositoryPath(t *testing.T) {
	// The most permissive legacy configuration still must not admit an account
	// that has to change its password.
	principal := Principal{
		Actor:              "user:reset",
		Role:               RoleMember,
		MustChangePassword: true,
		RepositoryPatterns: []string{"team/*"},
	}
	legacy := Authenticator{
		RepositoryReaders: nil,
		RepositoryWriters: map[string][]string{principal.Actor: {"team/releases"}},
	}
	if legacy.CanReadRepository(principal, "team/releases") ||
		legacy.CanReadMavenRepository(principal, "team") ||
		legacy.CanWriteMavenRepository(principal, "team/releases") {
		t.Fatal("must-change account reached a legacy repository path")
	}
	target := repository.HostedRepository{ID: "repo-id", Name: "team/releases"}
	authorizer := RepositoryAuthorizer{
		Legacy: legacy,
		Grants: grantStoreStub{set: repository.RepositoryGrantSet{
			Version: "2",
			Grants: []repository.RepositoryGrant{{
				Principal: principal.Actor, Scopes: []string{"repositories:admin"},
			}},
		}},
	}
	for _, operation := range []RepositoryOperation{RepositoryRead, RepositoryWrite, RepositoryAdmin, RepositoryIntelligence} {
		decision := authorizer.Authorize(context.Background(), principal, target, operation)
		if decision.Allowed || decision.Source != "role" || decision.Reason != "password_change_required" {
			t.Fatalf("operation=%s decision=%+v", operation, decision)
		}
		decision, managed := authorizer.ManagedResourceDecision(context.Background(), principal, target, operation, "")
		if !managed || decision.Allowed || decision.Reason != "password_change_required" {
			t.Fatalf("managed operation=%s decision=%+v managed=%t", operation, decision, managed)
		}
	}
}

func TestMemberRoleGrantsNoImplicitOperation(t *testing.T) {
	for _, operation := range []RepositoryOperation{RepositoryRead, RepositoryWrite, RepositoryAdmin, RepositoryIntelligence} {
		if RoleAllows(RoleMember, operation) {
			t.Fatalf("member implicitly allows %s", operation)
		}
	}
	if got := RoleFromRoles([]string{"member"}); got != RoleMember {
		t.Fatalf("RoleFromRoles(member)=%q", got)
	}
	// An administrator level still wins, so an existing multi-role credential
	// keeps the reach it is entitled to.
	if got := RoleFromRoles([]string{"member", "admin"}); got != RoleAdmin {
		t.Fatalf("RoleFromRoles(member,admin)=%q", got)
	}
	// The removed reader and writer values select nothing, alone or beside a
	// recognized level, so a stale credential cannot outrank the member level.
	for _, roles := range [][]string{{"reader"}, {"writer"}, {"reader", "writer"}, {"member", "writer"}, {"member", "reader"}} {
		if got := RoleFromRoles(roles); got != "" && got != RoleMember {
			t.Fatalf("RoleFromRoles(%v)=%q want empty or member", roles, got)
		}
	}
}

func TestUnmanagedGrantSetDecidesOnlyForNamedPrincipals(t *testing.T) {
	target := repository.HostedRepository{ID: "repo-id", Name: "releases"}
	authorizer := RepositoryAuthorizer{
		Grants: grantStoreStub{set: repository.RepositoryGrantSet{Version: "1", Grants: []repository.RepositoryGrant{
			{Principal: "named-reader", Scopes: []string{"repositories:read"}},
		}}},
		Legacy: Authenticator{RepositoryReaders: nil},
	}
	named := authorizer.Authorize(context.Background(), Principal{Actor: "named-reader"}, target, RepositoryRead)
	if !named.Allowed || named.Source != "repository_grants" || named.Reason != "scope_granted" {
		t.Fatalf("named read=%+v", named)
	}
	// The set names this principal but grants no write, so it decides against the
	// write instead of falling back to the permissive legacy default.
	if decision := authorizer.Authorize(context.Background(), Principal{Actor: "named-reader"}, target, RepositoryWrite); decision.Allowed || decision.Reason != "scope_not_granted" {
		t.Fatalf("named write=%+v", decision)
	}
	// A principal the set does not name leaves the set unmanaged, so legacy
	// static policy keeps deciding for it.
	if _, managed := authorizer.ManagedResourceDecision(context.Background(), Principal{Actor: "stranger", Role: RoleMember}, target, RepositoryRead, ""); managed {
		t.Fatal("an unmanaged set must stay unmanaged for principals it does not name")
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
		Principal{Actor: "ops-admin", Role: RoleAdmin},
		repo,
		RepositoryRead,
		"release/app.txt",
	)
	if !managed || !decision.Allowed || decision.Source != "role" || decision.Reason != "role_admin" {
		t.Fatalf("managed=%v decision=%#v", managed, decision)
	}
	// A member level, and a removed legacy level, must be decided by the grants
	// instead of by the role check.
	for _, role := range []Role{RoleMember, "writer", "reader"} {
		decision, managed := authorizer.ManagedResourceDecision(context.Background(), Principal{Actor: "member-user", Role: role}, repo, RepositoryRead, "release/app.txt")
		if !managed || decision.Allowed || decision.Source != "repository_grants" {
			t.Fatalf("role=%q managed=%v decision=%#v", role, managed, decision)
		}
	}
}

func TestRoleFromRolesPicksMostPrivileged(t *testing.T) {
	if got := RoleFromRoles([]string{"none", "member"}); got != RoleMember {
		t.Fatalf("none+member=%q want member", got)
	}
	if got := RoleFromRoles([]string{"member", "admin"}); got != RoleAdmin {
		t.Fatalf("member+admin=%q want admin", got)
	}
	if got := RoleFromRoles([]string{"writer", "admin"}); got != RoleAdmin {
		t.Fatalf("writer+admin=%q want admin", got)
	}
	if got := RoleFromRoles([]string{"unknown", "member"}); got != RoleMember {
		t.Fatalf("unknown+member=%q want member", got)
	}
	// The removed values are unrecognized: a credential holding only those
	// selects no role, which is what stops a stale key from reaching every
	// repository.
	if got := RoleFromRoles([]string{"reader", "writer"}); got != Role("") {
		t.Fatalf("reader+writer=%q want empty", got)
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

	// The administrator level is the only role-derived allow, and it is decided
	// before the denying grant set is consulted.
	admin := Principal{Actor: "k", Role: RoleAdmin}
	if d := authorizer.Authorize(context.Background(), admin, target, RepositoryRead); !d.Allowed || d.Source != "role" {
		t.Fatalf("admin read=%+v", d)
	}
	if d := authorizer.Authorize(context.Background(), admin, target, RepositoryWrite); !d.Allowed || d.Source != "role" {
		t.Fatalf("admin write=%+v", d)
	}

	// member, an empty role, and the removed legacy levels carry no role-derived
	// capability, so the denying grant set decides against them.
	for _, role := range []Role{RoleMember, "", "reader", "writer"} {
		for _, operation := range []RepositoryOperation{RepositoryRead, RepositoryWrite, RepositoryAdmin} {
			d := authorizer.Authorize(context.Background(), Principal{Actor: "k", Role: role}, target, operation)
			if d.Allowed {
				t.Fatalf("role=%q operation=%s allowed=%+v", role, operation, d)
			}
		}
	}
}

type perRepositoryGrantStub map[string]repository.RepositoryGrantSet

func (s perRepositoryGrantStub) GetRepositoryGrants(_ context.Context, repositoryID string) (repository.RepositoryGrantSet, error) {
	set, ok := s[repositoryID]
	if !ok {
		return repository.RepositoryGrantSet{}, repository.ErrNotFound
	}
	return set, nil
}

func (perRepositoryGrantStub) ReplaceRepositoryGrants(context.Context, string, []repository.RepositoryGrant, string) (repository.RepositoryGrantSet, error) {
	panic("unexpected ReplaceRepositoryGrants call")
}

// A level reaches no repository on its own, and a grant reaches exactly the
// repository it names, so the same account level is allowed on one repository
// and refused on the next for every operation.
func TestRepositoryAuthorizerScopesAuthorityToTheGrantedRepository(t *testing.T) {
	authorizer := RepositoryAuthorizer{
		Grants: perRepositoryGrantStub{
			"repo-read":  {Version: "2", Grants: []repository.RepositoryGrant{{Principal: "read-member", Scopes: []string{"repositories:read"}}}},
			"repo-admin": {Version: "2", Grants: []repository.RepositoryGrant{{Principal: "admin-member", Scopes: []string{"repositories:admin"}}}},
		},
		Legacy: Authenticator{RepositoryReaders: map[string][]string{}},
	}
	readGranted := repository.HostedRepository{ID: "repo-read", Name: "read-granted", Format: repository.FormatRaw, State: repository.RepositoryActive}
	adminGranted := repository.HostedRepository{ID: "repo-admin", Name: "admin-granted", Format: repository.FormatRaw, State: repository.RepositoryActive}
	operations := []RepositoryOperation{RepositoryRead, RepositoryWrite, RepositoryAdmin, RepositoryIntelligence}
	full := map[RepositoryOperation]bool{RepositoryRead: true, RepositoryWrite: true, RepositoryAdmin: true, RepositoryIntelligence: true}

	for _, tc := range []struct {
		name    string
		actor   string
		target  repository.HostedRepository
		allowed map[RepositoryOperation]bool
	}{
		{name: "read scope reaches its own repository only", actor: "read-member", target: readGranted, allowed: map[RepositoryOperation]bool{RepositoryRead: true}},
		{name: "read scope reaches no other repository", actor: "read-member", target: adminGranted},
		{name: "admin scope reaches its own repository fully", actor: "admin-member", target: adminGranted, allowed: full},
		{name: "admin scope reaches no other repository", actor: "admin-member", target: readGranted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, operation := range operations {
				decision := authorizer.Authorize(context.Background(), Principal{Actor: tc.actor, Role: RoleMember}, tc.target, operation)
				if decision.Allowed != tc.allowed[operation] {
					t.Fatalf("operation=%s allowed=%v decision=%+v", operation, tc.allowed[operation], decision)
				}
				if !tc.allowed[operation] && decision.Reason == "authorization_pending" {
					t.Fatalf("operation=%s was refused as pending rather than as unauthorized: %+v", operation, decision)
				}
			}
		})
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

	memberToken, memberID := makeKey(t, []string{"member"})
	mp, mok := authenticator.Authenticate("Bearer " + memberToken)
	if !mok || mp.Role != RoleMember || mp.Admin || mp.Actor != "api-key:"+memberID {
		t.Fatalf("member principal=%#v ok=%t", mp, mok)
	}
	adminToken, adminID := makeKey(t, []string{"admin"})
	ap, aok := authenticator.Authenticate("Bearer " + adminToken)
	if !aok || ap.Role != RoleAdmin || !ap.Admin || ap.Actor != "api-key:"+adminID {
		t.Fatalf("admin principal=%#v ok=%t", ap, aok)
	}

	// A key that still stores a removed legacy role authenticates, because the
	// key itself is valid, but selects no level and therefore reaches nothing:
	// only a grant can admit it now.
	legacyToken, legacyID := makeKey(t, []string{"writer"})
	lp, lok := authenticator.Authenticate("Bearer " + legacyToken)
	if !lok || lp.Role != "" || lp.Admin || lp.Actor != "api-key:"+legacyID {
		t.Fatalf("legacy principal=%#v ok=%t", lp, lok)
	}
	authenticator.RepositoryReaders = map[string][]string{}
	authenticator.RepositoryWriters = map[string][]string{}
	for _, principal := range []Principal{mp, lp} {
		if authenticator.CanReadRepository(principal, "any") || authenticator.CanWriteMavenRepository(principal, "any") {
			t.Fatalf("principal %#v reached a repository with a configured policy", principal)
		}
	}
	if !authenticator.CanReadRepository(ap, "any") || !authenticator.CanWriteMavenRepository(ap, "any") {
		t.Fatal("administrator credential was denied")
	}
}

func TestRemovedLegacyRoleValuesNeverAuthenticateOrAuthorize(t *testing.T) {
	store := repository.NewMemoryStore()
	authenticator := Authenticator{AdminToken: "admin-secret", ResolverToken: "resolver-secret", Users: store}

	// A stored account whose level is a removed value authenticates with a
	// recognized-by-nobody level and reaches no repository path of its own.
	if _, err := store.CreateUser(context.Background(), repository.User{ID: "stale-user", Name: "stale", Role: "writer"}); err != nil {
		t.Fatal(err)
	}
	session := authenticator.IssueUserSession("stale-user")
	principal, ok := authenticator.Authenticate("Bearer " + session)
	if !ok || principal.Admin || principal.Role != "writer" {
		t.Fatalf("stale account principal=%#v ok=%t", principal, ok)
	}
	if RoleAllows(principal.Role, RepositoryRead) || principal.CanReadRepository("releases") {
		t.Fatalf("a stored legacy level authorized access: %#v", principal)
	}

	// A protocol token that names a removed role is refused outright, so it
	// cannot be replayed after the constants are gone.
	protocolToken := authenticator.IssuePrincipalToken(Principal{
		Actor: "k", Role: "writer", AuthenticationKind: AuthenticationStaticResolver,
	})
	if _, ok := authenticator.Authenticate("Bearer " + protocolToken); ok {
		t.Fatal("a protocol token carrying a removed role authenticated")
	}
	// A browser session claim carrying a removed role is refused the same way.
	webSession := authenticator.IssueWebSession(Principal{
		Actor: "oidc:user", Role: "reader", AuthenticationKind: AuthenticationOIDC,
	})
	if _, ok := authenticator.Authenticate("Bearer " + webSession); ok {
		t.Fatal("a web session carrying a removed role authenticated")
	}
	// A session claim that names a removed role as an OIDC mapping target is
	// refused, so the mapping cannot be replayed into a capability.
	staleMapping := authenticator.IssueWebSession(Principal{
		Actor: "user:stale", Role: RoleMember, AuthenticationKind: AuthenticationOIDC,
		OIDCRoleMappings: []OIDCRoleMappingMatch{{ExternalRole: "artifact-writer", GatewayRole: "writer"}},
	})
	if _, ok := authenticator.Authenticate("Bearer " + staleMapping); ok {
		t.Fatal("a session carrying a removed mapping target authenticated")
	}
	// The recorders accept the levels that still exist.
	for _, role := range []Role{RoleNone, RoleMember, RoleAdmin} {
		if !validGatewayRole(role) {
			t.Fatalf("validGatewayRole(%q)=false", role)
		}
	}
	for _, mapping := range []OIDCRoleMappingMatch{{ExternalRole: "artifact-member", GatewayRole: RoleMember}, {ExternalRole: "artifact-admin", GatewayRole: RoleAdmin}} {
		if !validOIDCMetadata(AuthenticationOIDC, false, []OIDCRoleMappingMatch{mapping}) {
			t.Fatalf("valid mapping %+v was refused", mapping)
		}
	}
	for _, mapping := range []OIDCRoleMappingMatch{{ExternalRole: "artifact-reader", GatewayRole: "reader"}, {ExternalRole: "artifact-writer", GatewayRole: "writer"}} {
		if validOIDCMetadata(AuthenticationOIDC, false, []OIDCRoleMappingMatch{mapping}) {
			t.Fatalf("removed mapping target %+v was accepted", mapping)
		}
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

func TestAuthenticatorRejectsRevokedVersionedUserSession(t *testing.T) {
	store := repository.NewMemoryStore()
	user, err := store.CreateUser(context.Background(), repository.User{ID: "versioned-user", Name: "versioned", Role: string(RoleAdmin)})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{AdminToken: "admin-secret", Users: store}
	token := authenticator.IssueUserSession(user.ID, user.SessionVersion)
	if _, ok := authenticator.Authenticate("Bearer " + token); !ok {
		t.Fatal("fresh versioned user session was rejected")
	}
	if _, err := store.RevokeUserSessions(context.Background(), user.ID, user.Version); err != nil {
		t.Fatal(err)
	}
	if _, ok := authenticator.Authenticate("Bearer " + token); ok {
		t.Fatal("revoked versioned user session authenticated")
	}
}

func TestAuthenticatorChecksPersistedUserSessionIndependently(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	user, err := store.CreateUser(ctx, repository.User{ID: "session-user", Name: "session-user", Role: string(RoleAdmin)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err = store.CreateUserSession(ctx, repository.UserSession{
		ID: "active-session", UserID: user.ID, Kind: repository.UserSessionLocal,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateUserSession(ctx, repository.UserSession{
		ID: "expired-session", UserID: user.ID, Kind: repository.UserSessionLocal,
		CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{AdminToken: "admin-secret", Users: store, UserSessions: store}
	if _, ok := authenticator.Authenticate("Bearer " + authenticator.IssueUserSessionWithID(user.ID, "active-session")); !ok {
		t.Fatal("persisted session without an explicit version was rejected")
	}
	activeToken := authenticator.IssueUserSessionWithID(user.ID, "active-session", user.SessionVersion)
	principal, ok := authenticator.Authenticate("Bearer " + activeToken)
	if !ok || principal.UserID != user.ID || principal.SessionID != "active-session" {
		t.Fatalf("active principal=%+v ok=%v", principal, ok)
	}
	if _, ok = authenticator.Authenticate("Bearer " + authenticator.IssueUserSessionWithID(user.ID, "expired-session", user.SessionVersion)); ok {
		t.Fatal("expired persisted session authenticated")
	}
	if _, err = store.RevokeUserSession(ctx, user.ID, "active-session"); err != nil {
		t.Fatal(err)
	}
	if _, ok = authenticator.Authenticate("Bearer " + activeToken); ok {
		t.Fatal("individually revoked session authenticated")
	}
	if _, ok = authenticator.Authenticate("Bearer " + authenticator.IssueUserSession(user.ID, user.SessionVersion)); !ok {
		t.Fatal("legacy versioned session was not preserved")
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

func TestPasswordHashRoundTripRejectsInvalidCredentials(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("empty password was hashed")
	}
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct horse battery staple" || !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("password hash did not verify")
	}
	for _, candidate := range []struct{ hash, password string }{
		{hash, "wrong"},
		{"malformed", "correct horse battery staple"},
		{"", "correct horse battery staple"},
		{hash, ""},
	} {
		if VerifyPassword(candidate.hash, candidate.password) {
			t.Fatalf("invalid credential verified: hash=%q password=%q", candidate.hash, candidate.password)
		}
	}
}

func TestAuthenticatorWebSessionPreservesBoundedOIDCIdentity(t *testing.T) {
	authenticator := Authenticator{
		AdminToken: "admin-secret",
		RepositoryReaders: map[string][]string{
			"oidc:user-1": {"team/*"},
		},
	}
	expected := Principal{
		Actor:              "oidc:user-1",
		Admin:              true,
		Role:               RoleAdmin,
		AuthenticationKind: AuthenticationOIDC,
		OIDCAdminSubject:   true,
		OIDCRoleMappings: []OIDCRoleMappingMatch{
			{ExternalRole: "platform-admin", GatewayRole: RoleAdmin},
		},
	}
	token := authenticator.IssueWebSession(expected)
	principal, ok := authenticator.Authenticate("Bearer " + token)
	if !ok || principal.Actor != expected.Actor || !principal.Admin || principal.Role != RoleAdmin || principal.AuthenticationKind != AuthenticationOIDC || !principal.OIDCAdminSubject || len(principal.OIDCRoleMappings) != 1 || len(principal.RepositoryPatterns) != 1 {
		t.Fatalf("web session principal=%#v ok=%t", principal, ok)
	}

	parts := strings.Split(token, ".")
	parts[3] = base64.RawURLEncoding.EncodeToString([]byte("invalid signature"))
	if _, ok = authenticator.Authenticate("Bearer " + strings.Join(parts, ".")); ok {
		t.Fatal("tampered web session authenticated")
	}
	invalidMetadata := expected
	invalidMetadata.AuthenticationKind = AuthenticationStaticResolver
	if _, ok = authenticator.Authenticate("Bearer " + authenticator.IssueWebSession(invalidMetadata)); ok {
		t.Fatal("non-OIDC web session retained OIDC metadata")
	}
}

func TestAuthenticatorBoundOIDCWebSessionRechecksUserStateAndSessionVersion(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	user, err := store.CreateUser(ctx, repository.User{ID: "bound-user", Name: "bound", Role: string(RoleMember)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateUserIdentity(ctx, repository.UserIdentity{
		ID: "bound-identity", UserID: user.ID, Kind: repository.UserIdentityOIDC,
		Issuer: "https://issuer.example.test", Subject: "subject",
	}); err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{AdminToken: "admin-secret", UserIdentities: store, Users: store}
	token := authenticator.IssueWebSession(Principal{
		Actor: "user:bound", Role: RoleMember, AuthenticationKind: AuthenticationOIDC,
	}, user.SessionVersion)
	principal, ok := authenticator.Authenticate("Bearer " + token)
	if !ok || principal.Actor != "user:bound" || principal.Role != RoleMember || principal.Admin || principal.AuthenticationKind != AuthenticationOIDC {
		t.Fatalf("bound session principal=%#v ok=%v", principal, ok)
	}

	if _, err = store.RevokeUserSessions(ctx, user.ID, user.Version); err != nil {
		t.Fatal(err)
	}
	if _, ok = authenticator.Authenticate("Bearer " + token); ok {
		t.Fatal("revoked bound OIDC web session authenticated")
	}

	updated, err := store.GetUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateUser(ctx, repository.UserUpdate{ID: user.ID, State: ptrString(repository.UserDisabled)}, updated.Version); err != nil {
		t.Fatal(err)
	}
	if _, ok = authenticator.Authenticate("Bearer " + authenticator.IssueWebSession(Principal{
		Actor: "user:bound", Role: RoleMember, AuthenticationKind: AuthenticationOIDC,
	}, updated.SessionVersion)); ok {
		t.Fatal("disabled bound OIDC web session authenticated")
	}
}

func TestAuthenticatorBoundOIDCWebSessionHonorsRequiredPasswordChange(t *testing.T) {
	store := repository.NewMemoryStore()
	user, err := store.CreateUser(context.Background(), repository.User{
		ID: "password-change-user", Name: "password-change", Role: string(RoleAdmin),
		SecretHash: "local-hash", MustChangePassword: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := Authenticator{AdminToken: "admin-secret", Users: store}
	token := authenticator.IssueWebSession(Principal{
		Actor: "user:password-change", Role: RoleAdmin, Admin: true,
		AuthenticationKind: AuthenticationOIDC,
	}, user.SessionVersion)
	principal, ok := authenticator.Authenticate("Bearer " + token)
	if !ok || !principal.MustChangePassword || principal.Admin || principal.Role != "" || principal.AuthenticationKind != AuthenticationOIDC {
		t.Fatalf("required-password-change principal=%#v ok=%v", principal, ok)
	}
}

func ptrString(value string) *string { return &value }

func TestAuthenticatorMavenPoliciesKeepReadAndWriteSeparate(t *testing.T) {
	authenticator := Authenticator{
		RepositoryReaders: map[string][]string{"ci": {"team/*"}},
		RepositoryWriters: map[string][]string{"ci": {"releases", "snapshots/*"}},
	}
	principal := authenticator.PrincipalForActor("ci")
	if !authenticator.CanReadMavenRepository(principal, "team") {
		t.Fatal("group-shaped Maven read pattern was denied")
	}
	if !authenticator.CanWriteMavenRepository(principal, "releases") || !authenticator.CanWriteMavenRepository(principal, "snapshots/app") {
		t.Fatal("configured Maven writer pattern was denied")
	}
	if authenticator.CanWriteMavenRepository(principal, "team/app") {
		t.Fatal("read pattern unexpectedly granted Maven publication")
	}
	if (Principal{Role: RoleMember}).CanReadRepository("anything") {
		t.Fatal("member read a repository through its own authority")
	}
	if (Principal{Role: "writer"}).CanReadRepository("anything") {
		t.Fatal("a removed global writer role still read")
	}
	if !(Principal{Role: RoleAdmin}).CanReadRepository("anything") {
		t.Fatal("administrator role could not read")
	}
}

func TestLegacyReadDefaultPosture(t *testing.T) {
	// A configured deployment puts the actor's patterns on the principal, so a
	// pattern-less principal models a caller nothing grants access to.
	unmatched := Principal{Actor: "build-agent"}
	configured := Authenticator{RepositoryReaders: map[string][]string{"build-agent": {"releases"}}}

	// An unconfigured deployment denies an unmatched reader; the explicit opt-out
	// restores the pre-0.4 posture, and any configured policy stops the fallback.
	if (Authenticator{}).CanReadRepository(unmatched, "releases") {
		t.Fatal("the unconfigured posture must refuse an unmatched reader")
	}
	if !(Authenticator{LegacyReadPermissive: true}).CanReadRepository(unmatched, "releases") {
		t.Fatal("the explicit permissive posture must keep admitting readers")
	}
	if configured.CanReadRepository(unmatched, "releases") {
		t.Fatal("configuring any reader policy must stop the unrestricted fallback")
	}

	// Patterns decide on their own, whichever posture is in force.
	for _, authenticator := range []Authenticator{
		{RepositoryReaders: configured.RepositoryReaders},
		{RepositoryReaders: configured.RepositoryReaders, LegacyReadPermissive: true},
	} {
		granted := unmatched
		granted.RepositoryPatterns = []string{"releases"}
		if !authenticator.CanReadRepository(granted, "releases") {
			t.Fatal("a matching reader pattern was denied")
		}
		if authenticator.CanReadRepository(granted, "other") {
			t.Fatal("a non-matching reader pattern was admitted")
		}
	}

	// The permissive fallback must never override account state, or it would
	// undo the password-change and pending blocks.
	permissive := Authenticator{LegacyReadPermissive: true}
	for _, principal := range []Principal{
		{Actor: "user:reset", MustChangePassword: true},
		{Actor: "user:pending", Role: RoleNone},
	} {
		if permissive.CanReadRepository(principal, "releases") {
			t.Fatalf("account state must survive the permissive fallback: %+v", principal)
		}
	}
}

// A Group member path and a direct repository path must reach the same decision
// for the same principal, repository, and resource, so a Group read cannot
// drift from a direct read of one of its members.
func TestGroupMemberAndRepositoryDecisionsAgree(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "agreement", Name: "agreement", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "releases/"},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	authorizer := RepositoryAuthorizer{Grants: store, Legacy: Authenticator{RepositoryReaders: map[string][]string{}}}
	for _, tc := range []struct {
		name     string
		actor    string
		resource string
	}{
		{name: "granted principal inside the prefix", actor: "reader", resource: "releases/app.zip"},
		{name: "granted principal outside the prefix", actor: "reader", resource: "snapshots/app.zip"},
		{name: "ungranted principal", actor: "stranger", resource: "releases/app.zip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			principal := Principal{Actor: tc.actor}
			direct := authorizer.AuthorizeResource(ctx, principal, target, RepositoryRead, tc.resource)
			member, managed := ManagedGroupMemberDecision(ctx, store, authorizer, principal, repository.Member{RepositoryID: target.ID}, repository.FormatRaw, tc.resource)
			if !managed {
				t.Fatal("a bound member must be decided by its repository's grant set")
			}
			if member != direct {
				t.Fatalf("direct=%+v member=%+v", direct, member)
			}
		})
	}
}

func TestRepositoryAuthorizerMatchesScopesAndResourcePrefixes(t *testing.T) {
	target := repository.HostedRepository{ID: "repo-id", Name: "releases", Format: repository.FormatRaw, State: repository.RepositoryActive}
	authorizer := RepositoryAuthorizer{Grants: grantStoreStub{set: repository.RepositoryGrantSet{
		Version: "2",
		Grants: []repository.RepositoryGrant{
			{Principal: "reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "public/"},
			{Principal: "publisher", Scopes: []string{"repositories:write"}},
			{Principal: "owner", Scopes: []string{"repositories:admin"}},
		},
	}}}

	for _, tc := range []struct {
		actor    string
		op       RepositoryOperation
		resource string
		allowed  bool
	}{
		{"reader", RepositoryRead, "public/app.zip", true},
		{"reader", RepositoryRead, "private/app.zip", false},
		{"reader", RepositoryWrite, "public/app.zip", false},
		{"publisher", RepositoryRead, "", true},
		{"publisher", RepositoryWrite, "", true},
		{"owner", RepositoryAdmin, "", true},
	} {
		decision, managed := authorizer.ManagedResourceDecision(context.Background(), Principal{Actor: tc.actor}, target, tc.op, tc.resource)
		if !managed || decision.Allowed != tc.allowed {
			t.Errorf("actor=%s operation=%s resource=%s decision=%#v managed=%t", tc.actor, tc.op, tc.resource, decision, managed)
		}
	}

	fallback := RepositoryAuthorizer{
		LegacyFallback: func(Principal, repository.HostedRepository, RepositoryOperation) AuthorizationDecision {
			return AuthorizationDecision{Allowed: true, Source: "test_fallback", Reason: "allowed"}
		},
	}
	if decision := fallback.Authorize(context.Background(), Principal{Actor: "legacy"}, target, RepositoryRead); !decision.Allowed || decision.Source != "test_fallback" {
		t.Fatalf("legacy fallback decision=%#v", decision)
	}
}

func TestManagedGroupMemberDecisionValidatesRepositoryBinding(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "group-member", Name: "group-member", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, target.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}, ResourcePrefix: "widget"}}, "1"); err != nil {
		t.Fatal(err)
	}
	authorizer := RepositoryAuthorizer{Grants: store}
	decision, managed := ManagedGroupMemberDecision(ctx, store, authorizer, Principal{Actor: "reader"}, repository.Member{RepositoryID: target.ID}, repository.FormatPyPI, "widget/1.0.0")
	if !managed || !decision.Allowed {
		t.Fatalf("bound member decision=%#v managed=%t", decision, managed)
	}
	if _, managed = ManagedGroupMemberDecision(ctx, store, authorizer, Principal{Actor: "reader"}, repository.Member{}, repository.FormatPyPI, "widget"); managed {
		t.Fatal("unbound member unexpectedly used managed authorization")
	}
	decision, managed = ManagedGroupMemberDecision(ctx, nil, authorizer, Principal{Actor: "reader"}, repository.Member{RepositoryID: target.ID}, repository.FormatPyPI, "widget")
	if !managed || decision.Reason != "grant_lookup_failed" {
		t.Fatalf("missing repository store decision=%#v managed=%t", decision, managed)
	}
	decision, managed = ManagedGroupMemberDecision(ctx, store, authorizer, Principal{Actor: "reader"}, repository.Member{RepositoryID: target.ID}, repository.FormatNPM, "widget")
	if !managed || decision.Reason != "grant_lookup_failed" {
		t.Fatalf("format mismatch decision=%#v managed=%t", decision, managed)
	}
}
