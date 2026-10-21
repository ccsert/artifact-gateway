package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	discovery "github.com/ccsert/artifact-gateway/internal/ocidiscovery"
	"github.com/ccsert/artifact-gateway/internal/ocigraph"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
)

type OCIDiscoveryRequest struct{ Kind, Image, Subject, Next string }

type OCIDiscoveryClient interface {
	FetchOCIDiscovery(context.Context, repository.Member, OCIDiscoveryRequest) (*http.Response, error)
}

// FetchOCIDiscovery admits only a fixed registry metadata route. Pagination
// never expands egress permission or grants a CDN the registry credential.
func (c UpstreamClient) FetchOCIDiscovery(ctx context.Context, member repository.Member, in OCIDiscoveryRequest) (*http.Response, error) {
	base, err := url.Parse(strings.TrimRight(member.Endpoint, "/"))
	if err != nil || base == nil {
		return nil, errors.New("invalid OCI discovery source")
	}
	path, scope := base.Path+"/v2/_catalog", "registry:catalog:*"
	if in.Kind == "referrers" {
		if !ocigraph.ValidName(in.Image) || !ocigraph.ValidDigest(in.Subject) {
			return nil, errors.New("invalid OCI discovery identity")
		}
		path, scope = base.Path+"/v2/"+in.Image+"/referrers/"+in.Subject, "repository:"+in.Image+":pull"
	} else if in.Kind != "catalog" {
		return nil, errors.New("invalid OCI discovery kind")
	}
	endpoint := *base
	endpoint.Path, endpoint.RawPath, endpoint.RawQuery, endpoint.Fragment = path, "", "n=100", ""
	if in.Next != "" {
		if len(in.Next) > 4096 {
			return nil, discovery.ErrInvalid
		}
		next, err := url.Parse(in.Next)
		if err != nil {
			return nil, discovery.ErrInvalid
		}
		next = endpoint.ResolveReference(next)
		if !upstreamauth.SameOrigin(&endpoint, next) || next.User != nil || next.Path != path || next.RawPath != "" || next.Fragment != "" {
			return nil, discovery.ErrInvalid
		}
		query, err := url.ParseQuery(next.RawQuery)
		if err != nil {
			return nil, discovery.ErrInvalid
		}
		for key, values := range query {
			if len(values) != 1 || key != "n" && key != "last" {
				return nil, discovery.ErrInvalid
			}
		}
		endpoint.RawQuery = next.RawQuery
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if in.Kind == "referrers" {
		request.Header.Set("Accept", ocigraph.OCIIndex)
	}
	return c.fetchOCIWithScope(ctx, member, request, scope)
}

func ociDiscoveryNext(values []string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || len(values[0]) > 4096 {
		return "", errors.New("invalid OCI discovery link")
	}
	value := strings.TrimSpace(values[0])
	end := strings.IndexByte(value, '>')
	if !strings.HasPrefix(value, "<") || end < 2 || strings.TrimSpace(value[end+1:]) != `; rel="next"` {
		return "", errors.New("invalid OCI discovery link")
	}
	return value[1:end], nil
}
