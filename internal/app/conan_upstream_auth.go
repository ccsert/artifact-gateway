package app

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
)

// Conan Basic is an actual Registry API login, not Basic on artifact reads.
// Tokens are intentionally per-fetch: no token can survive rotation/clearing.
func conanUpstreamAuthorization(ctx context.Context, client *http.Client, member repository.Member) (string, error) {
	if member.UpstreamAuth == nil {
		return "", nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(member.Endpoint, "/")+"/v2/users/authenticate", nil)
	if err != nil {
		return "", upstreamauth.ErrCredential
	}
	if err := upstreamauth.Apply(request, member.UpstreamAuth); err != nil {
		return "", err
	}
	if member.UpstreamAuth.Scheme == repository.UpstreamAuthSchemeBearer {
		return request.Header.Get("Authorization"), nil
	}
	if member.UpstreamAuth.Scheme == repository.UpstreamAuthSchemeNone {
		return "", nil
	}
	response, err := client.Do(request)
	if err != nil {
		return "", upstreamauth.ErrCredential
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", upstreamauth.ErrCredential
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10+1))
	if err != nil || len(data) > 64<<10 {
		return "", upstreamauth.ErrCredential
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", upstreamauth.ErrCredential
	}
	for _, c := range token {
		valid := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~+/=", c)
		if !valid {
			return "", upstreamauth.ErrCredential
		}
	}
	return "Bearer " + token, nil
}
