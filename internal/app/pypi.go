package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
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
	if err := upstreamauth.ApplyOrigin(request, repo.Endpoint, repo.UpstreamAuth); err != nil {
		return nil, err
	}
	// All configured endpoints use HTTPS. Keep the existing explicit local
	// HTTP fixture seam; real upstreams share the bounded per-hop egress engine.
	if targetURL.Scheme == "https" {
		member := repository.Member{Type: repository.MemberProxy, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, EgressProxy: repo.EgressProxy}
		return c.doProxyUpstream(ctx, member, request)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	client.Jar = nil
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if !proxyUpstreamURLAllowed(repo, next.URL) {
			return errors.New("PyPI upstream redirect is not allowed")
		}
		upstreamauth.StripRedirect(next, via, repo.Endpoint)
		return nil
	}
	response, err := doProxyHTTP(client, request)
	if err != nil {
		return nil, errors.New("PyPI upstream unavailable")
	}
	return response, nil
}
