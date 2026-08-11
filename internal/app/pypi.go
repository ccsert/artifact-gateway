package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const pypiProxyUserAgent = "pip/26 Artifact-Gateway/1.0"

type PyPIClient interface {
	FetchPyPI(context.Context, string, repository.HostedRepository, string, http.Header) (*http.Response, error)
}

func (c UpstreamClient) FetchPyPI(ctx context.Context, method string, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	targetURL, err := url.Parse(target)
	if err != nil || !proxyUpstreamURLAllowed(repo, targetURL) {
		return nil, fmt.Errorf("PyPI upstream target is not allowed")
	}
	request, err := http.NewRequestWithContext(ctx, method, targetURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create PyPI upstream request: %w", err)
	}
	request.Header.Set("User-Agent", pypiProxyUserAgent)
	request.Header.Set("Accept", "text/html, application/vnd.pypi.simple.v1+json")
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
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
	client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
		if !proxyUpstreamURLAllowed(repo, next.URL) {
			return fmt.Errorf("PyPI upstream redirect is not allowed")
		}
		return nil
	}
	response, err := tracedHTTPClient(client).Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch PyPI upstream content: %w", err)
	}
	return response, nil
}
