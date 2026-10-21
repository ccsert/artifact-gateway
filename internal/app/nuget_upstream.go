package app

import (
	"context"
	"net/http"

	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
)

// FetchNuGet shares the established per-hop DNS pinning, explicit egress and
// redirect-origin boundary. Only protocol conditionals cross this adapter;
// local Basic/API-key/Bearer credentials never enter an upstream request.
func (c UpstreamClient) FetchNuGet(ctx context.Context, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	if err := upstreamauth.Validate(repo.UpstreamAuth); err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"Accept", "If-None-Match", "If-Modified-Since"} {
		if value := headers.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	if err = upstreamauth.ApplyOrigin(request, repo.Endpoint, repo.UpstreamAuth); err != nil {
		return nil, err
	}
	return c.doProxyUpstream(ctx, repository.Member{Type: repository.MemberProxy, Name: repo.Name, RepositoryID: repo.ID, RepositoryVersion: repo.Version, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, EgressProxy: repo.EgressProxy, UpstreamAuth: repo.UpstreamAuth}, request)
}
