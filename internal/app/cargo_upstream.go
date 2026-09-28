package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// FetchCargo applies the same endpoint, redirect and egress boundaries to
// index metadata, search responses and archive downloads.
func (c UpstreamClient) FetchCargo(ctx context.Context, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	u, err := url.Parse(target)
	if err != nil || !proxyUpstreamURLAllowed(repo, u) {
		return nil, errors.New("Cargo upstream target is not allowed")
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
	if err := applyUpstreamAuth(req, repo.UpstreamAuth); err != nil {
		return nil, errors.New("Cargo upstream credential unavailable")
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	if u.Scheme == "https" {
		client, err = egress.Apply(client, repo.EgressProxy, u.String(), rawEgressHooks())
		if err != nil {
			return nil, errors.New("Cargo upstream egress unavailable")
		}
	}
	origin := u.Host
	client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
		if !proxyUpstreamURLAllowed(repo, next.URL) {
			return errors.New("Cargo upstream redirect is not allowed")
		}
		if next.URL.Host != origin {
			next.Header.Del("Authorization")
		}
		return nil
	}
	response, err := tracedHTTPClient(client).Do(req)
	if err != nil {
		return nil, errors.New("Cargo upstream unavailable")
	}
	return response, nil
}
