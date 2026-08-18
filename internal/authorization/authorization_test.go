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
		ID: uuid.NewString(), Name: "management-client", SecretHash: HashAPIKey(token), Roles: []string{"reader"},
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
	user, err := store.CreateUser(ctx, repository.User{ID: "bound-user", Name: "bound", Role: string(RoleWriter)})
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
		Actor: "user:bound", Role: RoleWriter, AuthenticationKind: AuthenticationOIDC,
	}, user.SessionVersion)
	principal, ok := authenticator.Authenticate("Bearer " + token)
	if !ok || principal.Actor != "user:bound" || principal.Role != RoleWriter || principal.Admin || principal.AuthenticationKind != AuthenticationOIDC {
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
		Actor: "user:bound", Role: RoleWriter, AuthenticationKind: AuthenticationOIDC,
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
	if !(Principal{Role: RoleWriter}).CanReadRepository("anything", true) {
		t.Fatal("global writer role could not read")
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
