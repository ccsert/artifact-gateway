package app

import (
	"context"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/egress"
	rawformat "github.com/ccsert/artifact-gateway/internal/format/raw"
	rawprotocol "github.com/ccsert/artifact-gateway/internal/protocol/raw"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/requestcontext"
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
	return (rawformat.Upstream{HTTPClient: c.HTTPClient, Hooks: rawEgressHooks(), Trace: tracedHTTPClient}).FetchRaw(ctx, method, member, path, headers)
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
