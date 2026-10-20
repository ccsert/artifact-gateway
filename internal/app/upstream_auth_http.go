package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
)

// applyUpstreamAuth attaches the repository's configured credential to an
// upstream request. A repository without upstreamAuth reads anonymously. A
// stored credential that cannot be decrypted with the configured key is a hard
// failure: falling back to an anonymous request would turn a key-rotation
// mistake into silent, hard-to-diagnose 401s from the upstream.
func applyUpstreamAuth(request *http.Request, auth *repository.UpstreamAuth) error {
	if err := auth.Validate(); err != nil {
		return errors.New("open upstream credential: invalid configuration")
	}
	if auth == nil || auth.Scheme == repository.UpstreamAuthSchemeNone {
		return nil
	}
	secret, err := secrets.Open(upstreamAuthSecretPurpose, auth.Secret)
	if err != nil {
		return fmt.Errorf("open upstream credential: %w", err)
	}
	if secret == "" {
		return errors.New("open upstream credential: stored credential is empty")
	}
	switch auth.Scheme {
	case repository.UpstreamAuthSchemeBasic:
		request.SetBasicAuth(auth.Username, secret)
	case repository.UpstreamAuthSchemeBearer:
		request.Header.Set("Authorization", "Bearer "+secret)
	default:
		return fmt.Errorf("open upstream credential: unsupported scheme %q", auth.Scheme)
	}
	return nil
}

// upstreamCredentialIdentity changes on credential replacement or clearing and
// repository configuration CAS, including a return to anonymous reads. Only a
// digest of the encrypted record enters a persistent cache key or index.
func upstreamCredentialIdentity(member repository.Member) string {
	encoded, _ := json.Marshal([]any{member.RepositoryID, member.RepositoryVersion, member.UpstreamAuth})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func upstreamCacheNamespace(name string, members []repository.Member) string {
	identities := make([]string, 0, len(members))
	for _, member := range members {
		identities = append(identities, upstreamCredentialIdentity(member))
	}
	encoded, _ := json.Marshal(identities)
	digest := sha256.Sum256(encoded)
	return name + "\x00credentials:" + hex.EncodeToString(digest[:])
}
