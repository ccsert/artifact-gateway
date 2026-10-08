package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/requestcontext"
	"github.com/google/uuid"
)

var rawProxyLookupIP = net.DefaultResolver.LookupIP
var rawProxyDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}
var rawProxyFromEnvironment = http.ProxyFromEnvironment

const defaultRawMaxObjectBytes = int64(1 << 30)

type RawClient = rawprotocol.Client

// rawEgressHooks builds the egress hook set from the package-level injection
// points so tests keep their existing seams.
func rawEgressHooks() egress.Hooks {
	return egress.Hooks{LookupIP: rawProxyLookupIP, DialContext: rawProxyDialContext, ProxyFromEnvironment: rawProxyFromEnvironment}
}

func (c UpstreamClient) FetchRaw(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	client := c.HTTPClient
	if member.Type == repository.MemberProxy {
		var err error
		client, err = egress.Apply(client, member.EgressProxy, member.Endpoint, rawEgressHooks())
		if err != nil {
			return nil, &rawFetchError{code: "upstream_egress_failed", err: err}
		}
	}
	u, err := url.Parse(member.Endpoint)
	if err != nil {
		return nil, &rawFetchError{code: "upstream_configuration_invalid", err: fmt.Errorf("parse Raw endpoint: %w", err)}
	}
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		return nil, &rawFetchError{code: "upstream_configuration_invalid", err: fmt.Errorf("decode Raw path: %w", err)}
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + decodedPath
	u.RawPath = strings.TrimRight(u.EscapedPath(), "/") + "/" + path
	r, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, &rawFetchError{code: "upstream_configuration_invalid", err: fmt.Errorf("create Raw request: %w", err)}
	}
	// Raw has one cached file representation. Range and content negotiation are
	// applied locally after the complete canonical representation is fetched.
	client = tracedHTTPClient(client)
	// Never follow upstream redirects: a redirect can otherwise bypass the
	// configured proxy host allowlist (and may disclose hosted credentials).
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(r)
	if err != nil {
		return response, &rawFetchError{code: "upstream_transport_failed", err: err}
	}
	return response, nil
}

// rawProxyEgressClient returns a client that honors the configured HTTP(S)_PROXY
// for upstream requests. DNS resolution and the private-network check are
// delegated to the egress proxy, so no IP-pinning is applied locally.
func rawProxyEgressClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return egress.EnvironmentClient(client, rawEgressHooks())
}

func withRawAuditCorrelation(ctx context.Context, headerRequestID string) context.Context {
	correlated, _ := requestcontext.WithRequest(ctx, headerRequestID)
	return correlated
}

func rawAuditRequestID(ctx context.Context) string {
	if correlation, ok := requestcontext.FromContext(ctx); ok {
		return correlation.RequestID
	}
	return uuid.NewString()
}

func rawAuditTraceID(ctx context.Context) string {
	if correlation, ok := requestcontext.FromContext(ctx); ok {
		return correlation.TraceID
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}
