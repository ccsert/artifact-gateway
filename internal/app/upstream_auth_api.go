package app

import (
	"errors"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/secrets"
)

// upstreamAuthSecretPurpose binds every stored upstream credential ciphertext
// to this feature. Because internal/secrets uses the purpose as AEAD
// associated data, a ciphertext sealed for another purpose (a webhook signing
// key, an OIDC client secret) can never be replayed as an upstream credential.
const upstreamAuthSecretPurpose = "repository-upstream-auth"

// upstreamAuthRequest is the write shape for per-repository upstream registry
// credentials. Secret arrives as plaintext over TLS and is encrypted before
// storage; it never appears in responses. There is no separate "clear" flag:
// both supported schemes require a secret, so `scheme: none` is the only
// coherent way to remove a stored credential.
type upstreamAuthRequest struct {
	Scheme   repository.UpstreamAuthScheme `json:"scheme"`
	Username string                        `json:"username,omitempty"`
	Secret   string                        `json:"secret,omitempty"`
}

// upstreamAuthSupported reports whether a format/type pair consumes upstream
// credentials. The explicitly implemented native Proxy formats consume them. Rejecting the field
// everywhere else is deliberate: accepting a credential that the fetch path
// never reads would let an operator configure authentication that silently
// does nothing.
func upstreamAuthSupported(format repository.Format, repoType repository.RepositoryType) bool {
	if repoType != repository.RepositoryTypeProxy {
		return false
	}
	switch format {
	case repository.FormatGo, repository.FormatMaven, repository.FormatOCI, repository.FormatNPM, repository.FormatPyPI, repository.FormatCargo, repository.FormatRaw, repository.FormatAPT, repository.FormatConan:
		return true
	default:
		return false
	}
}

// resolveUpstreamAuth validates the request shape and encrypts the secret.
// existing carries the currently stored credential on update (nil on create);
// its secret survives only while the scheme is unchanged, so switching scheme
// requires resubmitting the secret instead of silently reusing the old one.
func resolveUpstreamAuth(request *upstreamAuthRequest, existing *repository.UpstreamAuth) (*repository.UpstreamAuth, error) {
	if request == nil {
		return existing, nil
	}
	if request.Scheme == repository.UpstreamAuthSchemeNone {
		if request.Username != "" || request.Secret != "" {
			return nil, errors.New("upstream authentication scheme none must not carry credentials")
		}
		return nil, nil
	}
	resolved := &repository.UpstreamAuth{Scheme: request.Scheme, Username: request.Username}
	switch {
	case request.Secret != "":
		encrypted, err := secrets.Seal(upstreamAuthSecretPurpose, request.Secret)
		if err != nil {
			return nil, err
		}
		resolved.Secret = encrypted
	case existing != nil && existing.Scheme == request.Scheme:
		resolved.Secret = existing.Secret
	}
	if err := resolved.Validate(); err != nil {
		return nil, err
	}
	return resolved, nil
}

// upstreamCredentialBindingChanged treats the complete configured endpoint and
// OCI issuer/audience as the authority boundary for a stored source credential.
func upstreamCredentialBindingChanged(existing, next repository.HostedRepository) bool {
	if existing.Endpoint != next.Endpoint {
		return true
	}
	if existing.Format != repository.FormatOCI {
		return false
	}
	if existing.OCIBearer == nil || next.OCIBearer == nil {
		return (existing.OCIBearer == nil) != (next.OCIBearer == nil)
	}
	return *existing.OCIBearer != *next.OCIBearer
}

func upstreamCredentialRebindNeedsSecret(existing, next repository.HostedRepository, request *upstreamAuthRequest) bool {
	if existing.UpstreamAuth == nil || existing.UpstreamAuth.Scheme == repository.UpstreamAuthSchemeNone || next.UpstreamAuth == nil || !upstreamCredentialBindingChanged(existing, next) {
		return false
	}
	return request == nil || request.Secret == ""
}

// redactUpstreamAuth strips the stored secret from a repository about to be
// encoded in a management response, replacing it with a configured marker. The
// copy keeps the stored value intact for the protocol paths.
func redactUpstreamAuth(repo repository.HostedRepository) repository.HostedRepository {
	if repo.UpstreamAuth == nil {
		return repo
	}
	redacted := *repo.UpstreamAuth
	redacted.CredentialsConfigured = redacted.Secret != ""
	redacted.Secret = ""
	repo.UpstreamAuth = &redacted
	return repo
}

// redactRepositorySecrets applies every credential redactor. Management
// responses must never be encoded without it.
func redactRepositorySecrets(repo repository.HostedRepository) repository.HostedRepository {
	return redactUpstreamAuth(redactEgressProxy(repo))
}
