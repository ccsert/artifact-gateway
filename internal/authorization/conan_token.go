package authorization

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
)

// Conan's Basic handshake exchanges a local credential for a short-lived,
// repository-bound token. It carries a credential hash, never the password;
// revocation and account state are rechecked for every request.
type conanTokenClaims struct {
	RepositoryID string             `json:"r"`
	Actor        string             `json:"a"`
	Credential   string             `json:"c"`
	Kind         AuthenticationKind `json:"k"`
	ExpiresAt    int64              `json:"e"`
}

func (a Authenticator) IssueConanToken(username, password, repositoryID string) (string, bool) {
	principal, ok := a.AuthenticateBasic(username, password)
	if !ok || len(a.ConanTokenSecret) < 32 || repositoryID == "" || principal.RepositoryAccessBlocked() {
		return "", false
	}
	claims, _ := json.Marshal(conanTokenClaims{RepositoryID: repositoryID, Actor: principal.Actor,
		Credential: HashAPIKey(password), Kind: principal.AuthenticationKind, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()})
	payload := "conan2." + base64.RawURLEncoding.EncodeToString(claims)
	mac := hmac.New(sha256.New, []byte(a.ConanTokenSecret))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), true
}

func (a Authenticator) AuthenticateConanToken(ctx context.Context, header, repositoryID string) (Principal, bool) {
	if ctx.Err() != nil {
		return Principal{}, false
	}
	parts := strings.Split(strings.TrimPrefix(header, "Bearer "), ".")
	if !strings.HasPrefix(header, "Bearer ") || len(parts) != 3 || parts[0] != "conan2" || len(a.ConanTokenSecret) < 32 {
		return Principal{}, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	mac := hmac.New(sha256.New, []byte(a.ConanTokenSecret))
	_, _ = mac.Write([]byte(strings.Join(parts[:2], ".")))
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return Principal{}, false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims conanTokenClaims
	if err != nil || json.Unmarshal(data, &claims) != nil || claims.RepositoryID != repositoryID || claims.Actor == "" || time.Now().Unix() >= claims.ExpiresAt {
		return Principal{}, false
	}
	switch claims.Kind {
	case AuthenticationStaticResolver:
		if !hmac.Equal([]byte(claims.Credential), []byte(HashAPIKey(a.ResolverToken))) {
			return Principal{}, false
		}
		return a.PrincipalForActor(claims.Actor), true
	case AuthenticationServiceAccountCredential:
		if a.APIKeys == nil || a.ServiceAccounts == nil {
			return Principal{}, false
		}
		key, err := a.APIKeys.FindActiveAPIKeyByHash(ctx, claims.Credential)
		if err != nil || key.ServiceAccountID == "" || claims.Actor != "service-account:"+key.ServiceAccountID {
			return Principal{}, false
		}
		account, err := a.ServiceAccounts.GetServiceAccount(ctx, key.ServiceAccountID)
		if err != nil || account.State != repository.ServiceAccountActive {
			return Principal{}, false
		}
		return Principal{Actor: claims.Actor, AuthenticationKind: claims.Kind}, true
	default:
		return Principal{}, false
	}
}
