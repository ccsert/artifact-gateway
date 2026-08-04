// Package authorization authenticates gateway principals and evaluates
// repository access policy without depending on an HTTP protocol.
package authorization

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type Principal struct {
	Actor              string
	Admin              bool
	Role               Role
	RepositoryPatterns []string
}

type Authenticator struct {
	AdminToken    string
	ResolverToken string
	AdminActor    string
	ResolverActor string
	// RepositoryReaders maps an actor to exact repository names or prefix
	// patterns ending in /*. A nil map keeps the local-development default.
	RepositoryReaders map[string][]string
	// RepositoryWriters is intentionally separate from readers: Maven deploy
	// must never turn a download grant into publication authority.
	RepositoryWriters map[string][]string
	OIDC              *OIDCValidator
	APIKeys           repository.APIKeyStore
	Users             repository.UserStore
}

func (a Authenticator) Authenticate(header string) (Principal, bool) {
	const bearer = "Bearer "
	if !strings.HasPrefix(header, bearer) {
		return Principal{}, false
	}
	token := strings.TrimPrefix(header, bearer)
	if tokenMatches(token, a.AdminToken) {
		return Principal{Actor: a.AdminActor, Admin: true}, true
	}
	if tokenMatches(token, a.ResolverToken) {
		return a.PrincipalForActor(a.ResolverActor), true
	}
	if principal, ok := a.principalToken(token); ok {
		return principal, true
	}
	if actor, ok := a.tokenActor(token); ok {
		principal, active := a.principalForTokenActor(actor)
		return principal, active
	}
	if a.APIKeys != nil {
		key, err := a.APIKeys.FindActiveAPIKeyByHash(context.Background(), HashAPIKey(token))
		if err == nil {
			role := RoleFromRoles(key.Roles)
			return Principal{Actor: "api-key:" + key.ID, Admin: role == RoleAdmin, Role: role}, true
		}
	}
	if userID, ok := a.userSessionActor(token); ok && a.Users != nil {
		user, err := a.Users.GetUser(context.Background(), userID)
		if err == nil && user.State == repository.UserActive {
			role := Role(user.Role)
			return Principal{Actor: "user:" + user.Name, Admin: role == RoleAdmin, Role: role}, true
		}
	}
	if a.OIDC != nil {
		if identity, ok := a.OIDC.Validate(context.Background(), token); ok {
			principal := a.PrincipalForActor(identity.Subject)
			principal.Admin = identity.Admin
			principal.Role = identity.Role
			return principal, true
		}
	}
	return Principal{}, false
}

func (a Authenticator) principalForTokenActor(actor string) (Principal, bool) {
	if !strings.HasPrefix(actor, "user:") || a.Users == nil {
		return a.PrincipalForActor(actor), true
	}
	name := strings.TrimPrefix(actor, "user:")
	user, err := a.Users.GetUserByName(context.Background(), name)
	if err != nil || user.State != repository.UserActive {
		return Principal{}, false
	}
	role := Role(user.Role)
	return Principal{Actor: actor, Admin: role == RoleAdmin, Role: role, RepositoryPatterns: a.RepositoryReaders[actor]}, true
}

// AuthenticateBasic validates the resolver credential used by Maven, Conan,
// and native protocol clients, then returns the credential's actor principal.
func (a Authenticator) AuthenticateBasic(username, password string) (Principal, bool) {
	if username == "" || !a.ResolverPasswordMatches(password) {
		return Principal{}, false
	}
	return a.PrincipalForActor(username), true
}

// ResolverPasswordMatches validates a resolver secret without exposing the
// secret or the constant-time comparison implementation to protocol packages.
func (a Authenticator) ResolverPasswordMatches(password string) bool {
	return tokenMatches(password, a.ResolverToken)
}

func (a Authenticator) PrincipalForActor(actor string) Principal {
	return Principal{Actor: actor, RepositoryPatterns: a.RepositoryReaders[actor]}
}

func (p Principal) CanReadRepository(repositoryName string, policyConfigured bool) bool {
	if p.Admin || RoleAllows(p.Role, RepositoryRead) || !policyConfigured {
		return true
	}
	for _, pattern := range p.RepositoryPatterns {
		if pattern == repositoryName || strings.HasSuffix(pattern, "/*") && strings.HasPrefix(repositoryName, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func (a Authenticator) CanReadRepository(principal Principal, repositoryName string) bool {
	return principal.CanReadRepository(repositoryName, a.RepositoryReaders != nil)
}

// CanReadMavenRepository treats a Maven Group as its repository boundary.
// `group/*` is accepted for compatibility with path-shaped grant policies,
// without broadening OCI's wildcard semantics.
func (a Authenticator) CanReadMavenRepository(principal Principal, groupName string) bool {
	if a.CanReadRepository(principal, groupName) {
		return true
	}
	for _, pattern := range principal.RepositoryPatterns {
		if strings.TrimSuffix(pattern, "/*") == groupName && strings.HasSuffix(pattern, "/*") {
			return true
		}
	}
	return false
}

func (a Authenticator) CanWriteMavenRepository(principal Principal, repositoryName string) bool {
	if principal.Admin || RoleAllows(principal.Role, RepositoryWrite) {
		return true
	}
	for _, pattern := range a.RepositoryWriters[principal.Actor] {
		if pattern == repositoryName || strings.HasSuffix(pattern, "/*") && strings.HasPrefix(repositoryName, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func (a Authenticator) IssueToken(actor string) string {
	expiresAt := time.Now().UTC().Add(5 * time.Minute).Unix()
	payload := "v1." + base64.RawURLEncoding.EncodeToString([]byte(actor)) + "." + strconv.FormatInt(expiresAt, 10)
	mac := hmac.New(sha256.New, []byte(a.ResolverToken))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// IssuePrincipalToken mints a short-lived protocol token that preserves the
// authenticated principal's role. Legacy actor-only tokens remain supported
// through IssueToken for existing clients and tests.
func (a Authenticator) IssuePrincipalToken(principal Principal) string {
	expiresAt := time.Now().UTC().Add(5 * time.Minute).Unix()
	claims, _ := json.Marshal(struct {
		Actor string `json:"a"`
		Role  Role   `json:"r,omitempty"`
		Admin bool   `json:"d,omitempty"`
	}{Actor: principal.Actor, Role: principal.Role, Admin: principal.Admin})
	payload := "v2." + base64.RawURLEncoding.EncodeToString(claims) + "." + strconv.FormatInt(expiresAt, 10)
	mac := hmac.New(sha256.New, []byte(a.ResolverToken))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// IssueUserSession mints a stateless session token for a local user. The token
// carries only the user id and an expiry; the user's current role and active
// state are rechecked on every authenticated request, so role changes and
// disabling take effect without a session store.
func (a Authenticator) IssueUserSession(userID string) string {
	expiresAt := time.Now().UTC().Add(12 * time.Hour).Unix()
	payload := "us." + base64.RawURLEncoding.EncodeToString([]byte(userID)) + "." + strconv.FormatInt(expiresAt, 10)
	mac := hmac.New(sha256.New, []byte(a.AdminToken))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a Authenticator) userSessionActor(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "us" || a.AdminToken == "" {
		return "", false
	}
	userID, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(userID) == 0 {
		return "", false
	}
	expiresAt, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().UTC().Unix() >= expiresAt {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(a.AdminToken))
	_, _ = mac.Write([]byte(strings.Join(parts[:3], ".")))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", false
	}
	return string(userID), true
}

func (a Authenticator) tokenActor(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v1" || a.ResolverToken == "" {
		return "", false
	}
	actor, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(actor) == 0 {
		return "", false
	}
	expiresAt, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().UTC().Unix() >= expiresAt {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(a.ResolverToken))
	_, _ = mac.Write([]byte(strings.Join(parts[:3], ".")))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", false
	}
	return string(actor), true
}

func (a Authenticator) principalToken(token string) (Principal, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v2" || a.ResolverToken == "" {
		return Principal{}, false
	}
	expiresAt, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().UTC().Unix() >= expiresAt {
		return Principal{}, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return Principal{}, false
	}
	mac := hmac.New(sha256.New, []byte(a.ResolverToken))
	_, _ = mac.Write([]byte(strings.Join(parts[:3], ".")))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return Principal{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Principal{}, false
	}
	var claims struct {
		Actor string `json:"a"`
		Role  Role   `json:"r"`
		Admin bool   `json:"d"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Actor == "" {
		return Principal{}, false
	}
	if strings.HasPrefix(claims.Actor, "user:") {
		return a.principalForTokenActor(claims.Actor)
	}
	if claims.Role != "" && claims.Role != RoleReader && claims.Role != RoleWriter && claims.Role != RoleAdmin {
		return Principal{}, false
	}
	return Principal{
		Actor:              claims.Actor,
		Admin:              claims.Admin,
		Role:               claims.Role,
		RepositoryPatterns: a.RepositoryReaders[claims.Actor],
	}, true
}

func tokenMatches(value, expected string) bool {
	return expected != "" && len(value) == len(expected) && subtle.ConstantTimeCompare([]byte(value), []byte(expected)) == 1
}

// HashAPIKey returns the database verifier for a generated high-entropy key.
// The plaintext key is never persisted.
func HashAPIKey(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func containsRole(roles []string, expected string) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}
