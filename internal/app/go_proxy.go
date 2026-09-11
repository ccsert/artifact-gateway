package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
)

const goProxyUserAgent = "Go-http-client/2.0 Artifact-Gateway/1.0"

type GoClient interface {
	FetchGo(context.Context, string, repository.HostedRepository, string, http.Header) (*http.Response, error)
}

func (c UpstreamClient) FetchGo(ctx context.Context, method string, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	targetURL, err := url.Parse(target)
	if err != nil || !proxyUpstreamURLAllowed(repo, targetURL) {
		return nil, fmt.Errorf("go upstream target is not allowed")
	}
	request, err := http.NewRequestWithContext(ctx, method, targetURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Go upstream request: %w", err)
	}
	request.Header.Set("User-Agent", goProxyUserAgent)
	request.Header.Set("Accept", "application/json, application/zip, text/plain")
	for _, name := range []string{"If-Modified-Since", "If-None-Match"} {
		if value := headers.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	if err := applyUpstreamAuth(request, repo.UpstreamAuth); err != nil {
		return nil, err
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	if targetURL.Scheme == "https" {
		client, err = egress.Apply(client, repo.EgressProxy, targetURL.String(), rawEgressHooks())
		if err != nil {
			return nil, err
		}
	}
	origin := targetURL.Host
	client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
		if !proxyUpstreamURLAllowed(repo, next.URL) {
			return fmt.Errorf("go upstream redirect is not allowed")
		}
		// A redirect to another host is a different trust boundary: never carry
		// the upstream credential across it, even to an allow-listed host.
		if next.URL.Host != origin {
			next.Header.Del("Authorization")
		}
		return nil
	}
	response, err := tracedHTTPClient(client).Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Go upstream content: %w", err)
	}
	return response, nil
}

// applyUpstreamAuth attaches the repository's configured credential to an
// upstream request. A repository without upstreamAuth reads anonymously. A
// stored credential that cannot be decrypted with the configured key is a hard
// failure: falling back to an anonymous request would turn a key-rotation
// mistake into silent, hard-to-diagnose 401s from the upstream.
func applyUpstreamAuth(request *http.Request, auth *repository.UpstreamAuth) error {
	if auth == nil || auth.Scheme == repository.UpstreamAuthSchemeNone {
		return nil
	}
	secret, err := secrets.Open(upstreamAuthSecretPurpose, auth.Secret)
	if err != nil {
		return fmt.Errorf("open upstream credential: %w", err)
	}
	if secret == "" {
		return fmt.Errorf("open upstream credential: stored credential is empty")
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
