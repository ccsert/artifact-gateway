package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const npmProxyUserAgent = "npm/11 Artifact-Gateway/1.0"

var errNPMUpstreamCircuitOpen = fmt.Errorf("npm upstream circuit is open")

type NPMClient interface {
	FetchNPM(context.Context, string, repository.HostedRepository, string, http.Header) (*http.Response, error)
}

type npmProxyProtection struct {
	coordinator OCICacheCoordinator
	breakerTTL  time.Duration
	mu          sync.Mutex
	openUntil   map[string]time.Time
}

func newNPMProxyProtection(coordinator OCICacheCoordinator, breakerTTL time.Duration) *npmProxyProtection {
	if breakerTTL <= 0 {
		breakerTTL = 30 * time.Second
	}
	return &npmProxyProtection{coordinator: coordinator, breakerTTL: breakerTTL, openUntil: make(map[string]time.Time)}
}

func (p *npmProxyProtection) allowed(ctx context.Context, endpoint string) bool {
	if p == nil {
		return true
	}
	if p.coordinator != nil {
		open, err := p.coordinator.CircuitOpen(ctx, "npm:"+endpoint)
		if err == nil && open {
			return false
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return !time.Now().UTC().Before(p.openUntil[endpoint])
}

func (p *npmProxyProtection) failure(ctx context.Context, endpoint string) {
	if p == nil {
		return
	}
	if p.coordinator != nil {
		_ = p.coordinator.OpenCircuit(ctx, "npm:"+endpoint, p.breakerTTL)
	}
	p.mu.Lock()
	p.openUntil[endpoint] = time.Now().UTC().Add(p.breakerTTL)
	p.mu.Unlock()
}

func (p *npmProxyProtection) success(ctx context.Context, endpoint string) {
	if p == nil {
		return
	}
	if p.coordinator != nil {
		_ = p.coordinator.CloseCircuit(ctx, "npm:"+endpoint)
	}
	p.mu.Lock()
	delete(p.openUntil, endpoint)
	p.mu.Unlock()
}

func (c UpstreamClient) FetchNPM(ctx context.Context, method string, repo repository.HostedRepository, target string, headers http.Header) (*http.Response, error) {
	targetURL, err := url.Parse(target)
	if err != nil || !npmUpstreamURLAllowed(repo, targetURL) {
		return nil, fmt.Errorf("npm upstream target is not allowed")
	}
	request, err := http.NewRequestWithContext(ctx, method, targetURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create npm upstream request: %w", err)
	}
	request.Header.Set("User-Agent", npmProxyUserAgent)
	for _, name := range []string{"Accept", "If-Modified-Since", "If-None-Match"} {
		if value := headers.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		copy := *client
		client = &copy
	}
	// Repository endpoints are configured as HTTPS by the management API.
	// Tests may construct local HTTP repositories directly, so leave those
	// unsupported fixtures on their injected transport. Every supported HTTPS
	// endpoint, including allowed CDN tarball hosts, is pinned or proxied using
	// the actual target URL to prevent DNS rebinding.
	if targetURL.Scheme == "https" {
		client, err = egress.Apply(client, repo.EgressProxy, targetURL.String(), rawEgressHooks())
		if err != nil {
			return nil, err
		}
	}
	client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
		if !npmUpstreamURLAllowed(repo, next.URL) {
			return fmt.Errorf("npm upstream redirect is not allowed")
		}
		return nil
	}
	response, err := tracedHTTPClient(client).Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch npm upstream content: %w", err)
	}
	return response, nil
}

func npmUpstreamURLAllowed(repo repository.HostedRepository, target *url.URL) bool {
	endpoint, err := url.Parse(repo.Endpoint)
	if err != nil || endpoint.Hostname() == "" || target == nil || target.User != nil || target.Hostname() == "" {
		return false
	}
	if endpoint.Scheme == "https" {
		if target.Scheme != "https" {
			return false
		}
	} else if endpoint.Scheme != "http" || target.Scheme != "http" {
		return false
	}
	targetHost := strings.ToLower(target.Hostname())
	if targetHost == strings.ToLower(endpoint.Hostname()) {
		return true
	}
	for _, allowed := range repo.AllowedHosts {
		allowedURL, parseErr := url.Parse("//" + strings.TrimSpace(allowed))
		if parseErr == nil && strings.EqualFold(allowedURL.Hostname(), targetHost) {
			return true
		}
	}
	return false
}
