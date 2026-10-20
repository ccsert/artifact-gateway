package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
)

// FetchCargo applies the same endpoint, redirect and egress boundaries to
// index metadata, search responses and archive downloads.
func (c UpstreamClient) FetchCargo(ctx context.Context, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	u, err := url.Parse(target)
	if err != nil || !proxyUpstreamURLAllowed(repo, u) {
		return nil, errors.New("cargo upstream target is not allowed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("invalid Cargo upstream request")
	}
	req.Header.Set("User-Agent", "Cargo/1.0 Artifact-Gateway/1.0")
	for _, name := range []string{"If-None-Match", "If-Modified-Since"} {
		if v := headers.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	if err := upstreamauth.ApplyOrigin(req, repo.Endpoint, repo.UpstreamAuth); err != nil {
		return nil, upstreamauth.ErrCredential
	}
	// All configured endpoints use HTTPS. Keep the existing explicit local
	// HTTP fixture seam; real upstreams share the bounded per-hop egress engine.
	if u.Scheme == "https" {
		member := repository.Member{Type: repository.MemberProxy, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, EgressProxy: repo.EgressProxy}
		return c.doProxyUpstream(ctx, member, req)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	client.Jar = nil
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if !proxyUpstreamURLAllowed(repo, next.URL) {
			return errors.New("cargo upstream redirect is not allowed")
		}
		upstreamauth.StripRedirect(next, via, repo.Endpoint)
		return nil
	}
	response, err := doProxyHTTP(client, req)
	if err != nil {
		return nil, errors.New("cargo upstream unavailable")
	}
	return response, nil
}
