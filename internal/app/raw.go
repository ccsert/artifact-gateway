package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

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

func (c UpstreamClient) FetchRaw(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	client := c.HTTPClient
	if member.Type == repository.MemberProxy {
		if rawProxyTLSOverride(client) {
			return nil, errors.New("raw proxy HTTP client must not override TLS dialing")
		}
		useEgressProxy, err := rawProxyApplies(member.Endpoint)
		if err != nil {
			return nil, err
		}
		if useEgressProxy {
			// 走 HTTP(S)_PROXY 时，由代理服务器负责 DNS 解析与 outbound 连接，
			// IP-pinning 既不必要也会绕过代理，因此改用标准代理 Transport。
			client = rawProxyEgressClient(client)
		} else {
			u, ips, err := resolveRawProxyEndpoint(ctx, member.Endpoint)
			if err != nil {
				return nil, err
			}
			client, err = rawProxyHTTPClient(client, u.Hostname(), u.Port(), ips)
			if err != nil {
				return nil, err
			}
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

func resolveRawProxyEndpoint(ctx context.Context, endpoint string) (*url.URL, []net.IP, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return nil, nil, errors.New("raw proxy endpoint is not a valid HTTPS URL")
	}
	ips, err := rawProxyLookupIP(ctx, "ip", u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, nil, fmt.Errorf("resolve Raw proxy endpoint: %w", err)
	}
	for _, ip := range ips {
		if privateAddress(ip) {
			return nil, nil, errors.New("raw proxy endpoint resolves to a private address")
		}
	}
	return u, ips, nil
}

// rawProxyApplies asks the standard proxy policy whether this endpoint will use
// an egress proxy. A configured proxy may still be bypassed by NO_PROXY.
func rawProxyApplies(endpoint string) (bool, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false, fmt.Errorf("parse Raw proxy endpoint: %w", err)
	}
	proxy, err := rawProxyFromEnvironment(&http.Request{URL: u})
	if err != nil {
		return false, fmt.Errorf("resolve egress proxy: %w", err)
	}
	return proxy != nil, nil
}

// rawProxyEgressClient returns a client that honors the configured HTTP(S)_PROXY
// for upstream requests. DNS resolution and the private-network check are
// delegated to the egress proxy, so no IP-pinning is applied locally.
func rawProxyEgressClient(client *http.Client) *http.Client {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		transport = transport.Clone()
	}
	transport.Proxy = http.ProxyFromEnvironment
	// An injected dial hook can bypass the selected egress proxy. Keep TLS
	// trust configuration but let the standard transport establish the proxy hop.
	transport.DialContext = nil
	transport.Dial = nil //nolint:staticcheck // Clear the legacy hook too; otherwise it bypasses the egress proxy when DialContext is nil.
	copy := *client
	copy.Transport = transport
	return &copy
}

// rawProxyHTTPClient pins the TCP connection to the address that passed the
// private-network check, preventing a second DNS resolution from rebinding it.
func rawProxyHTTPClient(client *http.Client, hostname, port string, ips []net.IP) (*http.Client, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	base, ok := transport.(*http.Transport)
	if !ok {
		return nil, errors.New("raw proxy HTTP client must use *http.Transport")
	}
	if rawProxyTLSOverride(client) {
		return nil, errors.New("raw proxy HTTP client must not override TLS dialing")
	}
	copy := *client
	pinnedTransport := base.Clone()
	pinnedTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		requestHost, requestPort, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(requestHost, hostname) {
			return nil, errors.New("raw proxy dial target changed")
		}
		if port != "" {
			requestPort = port
		}
		var lastErr error
		for _, ip := range ips {
			connection, err := rawProxyDialContext(ctx, network, net.JoinHostPort(ip.String(), requestPort))
			if err == nil {
				return connection, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	copy.Transport = pinnedTransport
	return &copy, nil
}

func rawProxyTLSOverride(client *http.Client) bool {
	if client == nil {
		return false
	}
	base, ok := client.Transport.(*http.Transport)
	return ok && base != nil && (base.DialTLSContext != nil || transportHasLegacyDialTLS(base))
}

// DialTLS is deprecated but an installed legacy hook must still be rejected:
// it would bypass the pinned connection path below.
func transportHasLegacyDialTLS(transport *http.Transport) bool {
	field := reflect.ValueOf(transport).Elem().FieldByName("DialTLS")
	return field.IsValid() && !field.IsNil()
}

func privateAddress(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()
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
