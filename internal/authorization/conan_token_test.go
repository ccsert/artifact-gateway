package authorization

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestConanTokenBindsRepositoryAndCredentialRevocation(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	account, err := store.CreateServiceAccount(ctx, repository.ServiceAccount{ID: uuid.NewString(), Name: "conan-publisher"})
	if err != nil {
		t.Fatal(err)
	}
	auth := Authenticator{ResolverToken: "local-signing-secret", ConanTokenSecret: strings.Repeat("c", 32), APIKeys: store, ServiceAccounts: store}
	const first = "agc_first_conan_credential"
	const second = "agc_rotated_conan_credential"
	key, err := store.CreateServiceAccountCredential(ctx, repository.APIKey{ID: uuid.NewString(), Name: "first", ServiceAccountID: account.ID, SecretHash: HashAPIKey(first)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateServiceAccountCredential(ctx, repository.APIKey{ID: uuid.NewString(), Name: "second", ServiceAccountID: account.ID, SecretHash: HashAPIKey(second)}); err != nil {
		t.Fatal(err)
	}
	token, ok := auth.IssueConanToken("ci", first, "repo")
	if !ok {
		t.Fatal("valid local machine credential rejected")
	}
	claims, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	if err != nil || strings.Contains(string(claims), first) {
		t.Fatal("credential plaintext leaked into protocol token")
	}
	if principal, ok := auth.AuthenticateConanToken(ctx, "Bearer "+token, "repo"); !ok || principal.Actor != "service-account:"+account.ID || principal.Admin {
		t.Fatalf("principal=%+v authenticated=%t", principal, ok)
	}
	for _, header := range []string{"Bearer " + token + "x", "Bearer " + strings.Replace(token, "conan2.", "v2.", 1), "Bearer wrong"} {
		if _, ok := auth.AuthenticateConanToken(ctx, header, "repo"); ok {
			t.Fatal("malformed token accepted")
		}
	}
	if _, ok := auth.AuthenticateConanToken(ctx, "Bearer "+token, "other"); ok {
		t.Fatal("token crossed repository boundary")
	}
	if _, err = store.RevokeServiceAccountCredential(ctx, account.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.AuthenticateConanToken(ctx, "Bearer "+token, "repo"); ok {
		t.Fatal("revoked credential still authorized its exchanged token")
	}
	rotated, ok := auth.IssueConanToken("ci", second, "repo")
	if !ok {
		t.Fatal("overlapping rotated credential rejected")
	}
	disabled := repository.ServiceAccountDisabled
	if _, err = store.UpdateServiceAccount(ctx, repository.ServiceAccountUpdate{ID: account.ID, State: &disabled}, account.Version); err != nil {
		t.Fatal(err)
	}
	if _, ok := auth.AuthenticateConanToken(ctx, "Bearer "+rotated, "repo"); ok {
		t.Fatal("disabled machine account still authorized protocol token")
	}
	if _, ok := auth.IssueConanToken("ci", first, "repo"); ok {
		t.Fatal("revoked credential exchanged again")
	}
}

func TestConanTokenStaticSigningKeyRotationAndInvalidHandshake(t *testing.T) {
	auth := Authenticator{ResolverToken: "local-secret", ConanTokenSecret: strings.Repeat("c", 32)}
	token, ok := auth.IssueConanToken("ci", "local-secret", "repo")
	if !ok {
		t.Fatal("valid resolver handshake rejected")
	}
	if _, ok := auth.AuthenticateConanToken(context.Background(), "Bearer "+token, "repo"); !ok {
		t.Fatal("protocol token rejected")
	}
	auth.ConanTokenSecret = strings.Repeat("d", 32)
	if _, ok := auth.AuthenticateConanToken(context.Background(), "Bearer "+token, "repo"); ok {
		t.Fatal("token survived protocol signing key rotation")
	}
	auth.ConanTokenSecret = strings.Repeat("c", 32)
	auth.ResolverToken = "rotated-local-secret"
	if _, ok := auth.AuthenticateConanToken(context.Background(), "Bearer "+token, "repo"); ok {
		t.Fatal("token survived signing key rotation")
	}
	for _, value := range []struct{ username, password, repo string }{{"ci", "wrong", "repo"}, {"", "rotated-local-secret", "repo"}, {"ci", "rotated-local-secret", ""}} {
		if _, ok := auth.IssueConanToken(value.username, value.password, value.repo); ok {
			t.Fatal("invalid handshake accepted")
		}
	}
}

func TestConanTokenRejectsSignedExpiredClaimsAndCancelledContext(t *testing.T) {
	auth := Authenticator{ResolverToken: "local-secret", ConanTokenSecret: strings.Repeat("c", 32)}
	claims, err := json.Marshal(conanTokenClaims{RepositoryID: "repo", Actor: "ci", Credential: HashAPIKey(auth.ResolverToken), Kind: AuthenticationStaticResolver, ExpiresAt: time.Now().Add(-time.Second).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	payload := "conan2." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(auth.ConanTokenSecret))
	_, _ = mac.Write([]byte(payload))
	token := payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if _, ok := auth.AuthenticateConanToken(context.Background(), "Bearer "+token, "repo"); ok {
		t.Fatal("signed expired token accepted")
	}
	token, ok := auth.IssueConanToken("ci", auth.ResolverToken, "repo")
	if !ok {
		t.Fatal("valid credential rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := auth.AuthenticateConanToken(ctx, "Bearer "+token, "repo"); ok {
		t.Fatal("cancelled credential validation accepted")
	}
}
