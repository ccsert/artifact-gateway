// Package upstreamauth owns protected repository credentials at outbound HTTP
// and cache identity seams. It does not construct stores or transports.
package upstreamauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
)

const SecretPurpose = "repository-upstream-auth"

var ErrCredential = errors.New("upstream credential unavailable")

// Validate also runs on cache hits so a missing encryption key or corrupted
// credential can never be hidden by a previously authorized cache entry.
func Validate(auth *repository.UpstreamAuth) error {
	return Apply(&http.Request{Header: make(http.Header)}, auth)
}

func Apply(request *http.Request, auth *repository.UpstreamAuth) error {
	if err := auth.Validate(); err != nil {
		return ErrCredential
	}
	if auth == nil || auth.Scheme == repository.UpstreamAuthSchemeNone {
		return nil
	}
	secret, err := secrets.Open(SecretPurpose, auth.Secret)
	if err != nil || secret == "" {
		return ErrCredential
	}
	switch auth.Scheme {
	case repository.UpstreamAuthSchemeBasic:
		request.SetBasicAuth(auth.Username, secret)
	case repository.UpstreamAuthSchemeBearer:
		request.Header.Set("Authorization", "Bearer "+secret)
	default:
		return ErrCredential
	}
	return nil
}

func SameOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

// ApplyOrigin never sends a repository credential to an allowed asset CDN.
// Egress permission does not grant that destination access to source secrets.
func ApplyOrigin(request *http.Request, endpoint string, auth *repository.UpstreamAuth) error {
	origin, err := url.Parse(endpoint)
	if err != nil || origin.Host == "" {
		return ErrCredential
	}
	if !SameOrigin(origin, request.URL) {
		return nil
	}
	return Apply(request, auth)
}

// StripRedirect follows the entire redirect chain. A return to the original
// origin cannot recover a credential stripped at an earlier hop.
func StripRedirect(next *http.Request, via []*http.Request, endpoint string) {
	origin, err := url.Parse(endpoint)
	strip := err != nil || !SameOrigin(origin, next.URL)
	for _, previous := range via {
		if !SameOrigin(origin, previous.URL) {
			strip = true
		}
	}
	if strip {
		next.Header.Del("Authorization")
	}
	// Forward-proxy credentials are owned by the transport, never a redirect.
	next.Header.Del("Proxy-Authorization")
}

// Namespace includes configuration version even after credentials are cleared.
// Neither plaintext nor ciphertext is placed in the cache index or its URL.
func Namespace(name string, member repository.Member) string {
	encoded, _ := json.Marshal([]any{member.RepositoryID, member.RepositoryVersion, member.UpstreamAuth})
	sum := sha256.Sum256(encoded)
	return name + "\x00credentials:" + hex.EncodeToString(sum[:])
}
