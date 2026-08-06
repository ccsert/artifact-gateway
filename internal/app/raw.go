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
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
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

// customEgressConfigured reports whether the member carries a per-repository
// egress override (direct or custom). Nil and environment keep the
// transport's default HTTP(S)_PROXY behavior, which the standard library
// already honors for OCI and Maven upstream fetches.
func customEgressConfigured(proxy *repository.EgressProxy) bool {
	return proxy != nil && (proxy.Mode == repository.EgressProxyModeDirect || proxy.Mode == repository.EgressProxyModeCustom)
}

func (c UpstreamClient) FetchRaw(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	client := c.HTTPClient
	if member.Type == repository.MemberProxy {
		var err error
		client, err = egress.Apply(client, member.EgressProxy, member.Endpoint, rawEgressHooks())
		if err != nil {
			return nil, err
		}
	}
	u, err := url.Parse(member.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse Raw endpoint: %w", err)
	}
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		return nil, fmt.Errorf("decode Raw path: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + decodedPath
	u.RawPath = strings.TrimRight(u.EscapedPath(), "/") + "/" + path
	r, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Raw request: %w", err)
	}
	// Raw has one cached file representation. Range and content negotiation are
	// applied locally after the complete canonical representation is fetched.
	client = tracedHTTPClient(client)
	// Never follow upstream redirects: a redirect can otherwise bypass the
	// configured proxy host allowlist (and may disclose hosted credentials).
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(r)
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

type rawAuditCorrelation struct{ requestID, traceID string }
type rawAuditCorrelationKey struct{}

func withRawAuditCorrelation(ctx context.Context, headerRequestID string) context.Context {
	requestID := safeRawCorrelationID(headerRequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	traceID := trace.SpanContextFromContext(ctx).TraceID().String()
	if traceID == "00000000000000000000000000000000" || traceID == "" {
		traceID = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	return context.WithValue(ctx, rawAuditCorrelationKey{}, rawAuditCorrelation{requestID: requestID, traceID: traceID})
}

func safeRawCorrelationID(value string) string {
	if len(value) == 0 || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-' {
			return ""
		}
	}
	return value
}

func rawAuditRequestID(ctx context.Context) string {
	if correlation, ok := ctx.Value(rawAuditCorrelationKey{}).(rawAuditCorrelation); ok {
		return correlation.requestID
	}
	return uuid.NewString()
}

func rawAuditTraceID(ctx context.Context) string {
	if correlation, ok := ctx.Value(rawAuditCorrelationKey{}).(rawAuditCorrelation); ok {
		return correlation.traceID
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}
