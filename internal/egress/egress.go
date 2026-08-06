// Package egress builds HTTP clients that honor a Proxy Repository's egress
// proxy configuration: direct connections with SSRF pinning, process-level
// environment proxies, or a per-repository custom HTTP CONNECT / SOCKS5
// proxy. See docs/proxy-egress-design.md.
package egress

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

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	xproxy "golang.org/x/net/proxy"
)

// Hooks captures the low-level network operations the factory depends on so
// protocol packages (and tests) can inject their own implementations.
type Hooks struct {
	LookupIP             func(ctx context.Context, network, host string) ([]net.IP, error)
	DialContext          func(ctx context.Context, network, address string) (net.Conn, error)
	ProxyFromEnvironment func(*http.Request) (*url.URL, error)
	// AllowPrivateProxy permits proxy addresses on private/loopback networks.
	// Reserved for tests and local development overrides.
	AllowPrivateProxy bool
}

// DefaultHooks returns the production hook set.
func DefaultHooks() Hooks {
	return Hooks{
		LookupIP: net.DefaultResolver.LookupIP,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
		ProxyFromEnvironment: http.ProxyFromEnvironment,
	}
}

// Apply returns a client whose transport honors the repository's egress proxy
// configuration for requests to endpoint. A nil proxy (or empty mode) keeps
// the legacy environment behavior.
func Apply(client *http.Client, proxy *repository.EgressProxy, endpoint string, hooks Hooks) (*http.Client, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if HasTLSOverride(client) {
		return nil, errors.New("egress client must not override TLS dialing")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return nil, errors.New("egress endpoint is not a valid HTTPS URL")
	}
	mode := repository.EgressProxyModeEnvironment
	if proxy != nil && proxy.Mode != "" {
		mode = proxy.Mode
	}
	switch mode {
	case repository.EgressProxyModeDirect:
		return PinnedClient(client, u, hooks)
	case repository.EgressProxyModeEnvironment:
		useEgressProxy, err := Applies(u, hooks)
		if err != nil {
			return nil, err
		}
		if useEgressProxy {
			// 走 HTTP(S)_PROXY 时，由代理服务器负责 DNS 解析与 outbound 连接，
			// IP-pinning 既不必要也会绕过代理，因此改用标准代理 Transport。
			return EnvironmentClient(client, hooks), nil
		}
		return PinnedClient(client, u, hooks)
	case repository.EgressProxyModeCustom:
		if bypassedByNoProxy(u.Hostname(), proxy.NoProxy) {
			return PinnedClient(client, u, hooks)
		}
		return customClient(client, proxy, u, hooks)
	default:
		return nil, fmt.Errorf("unsupported egress proxy mode %q", mode)
	}
}

// Applies asks the environment proxy policy whether this endpoint will use an
// egress proxy. A configured proxy may still be bypassed by NO_PROXY.
func Applies(u *url.URL, hooks Hooks) (bool, error) {
	proxy, err := hooks.ProxyFromEnvironment(&http.Request{URL: u})
	if err != nil {
		return false, fmt.Errorf("resolve egress proxy: %w", err)
	}
	return proxy != nil, nil
}

// EnvironmentClient returns a client that honors the configured HTTP(S)_PROXY
// for upstream requests. DNS resolution and the private-network check are
// delegated to the egress proxy, so no IP-pinning is applied locally.
func EnvironmentClient(client *http.Client, hooks Hooks) *http.Client {
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	} else {
		transport = transport.Clone()
	}
	transport.Proxy = hooks.ProxyFromEnvironment
	// An injected dial hook can bypass the selected egress proxy. Keep TLS
	// trust configuration but let the standard transport establish the proxy hop.
	transport.DialContext = nil
	transport.Dial = nil //nolint:staticcheck // Clear the legacy hook too; otherwise it bypasses the egress proxy when DialContext is nil.
	copy := *client
	copy.Transport = transport
	return &copy
}

// PinnedClient pins the TCP connection to the endpoint addresses that passed
// the private-network check, preventing a second DNS resolution from
// rebinding the connection.
func PinnedClient(client *http.Client, u *url.URL, hooks Hooks) (*http.Client, error) {
	ips, err := hooks.LookupIP(context.Background(), "ip", u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("resolve egress endpoint: %w", err)
	}
	for _, ip := range ips {
		if PrivateAddress(ip) {
			return nil, errors.New("egress endpoint resolves to a private address")
		}
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	base, ok := transport.(*http.Transport)
	if !ok {
		return nil, errors.New("egress HTTP client must use *http.Transport")
	}
	hostname, port := u.Hostname(), u.Port()
	copy := *client
	pinnedTransport := base.Clone()
	pinnedTransport.Proxy = nil
	pinnedTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		requestHost, requestPort, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(requestHost, hostname) {
			return nil, errors.New("egress dial target changed")
		}
		if port != "" {
			requestPort = port
		}
		var lastErr error
		for _, ip := range ips {
			connection, err := hooks.DialContext(ctx, network, net.JoinHostPort(ip.String(), requestPort))
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

// customClient routes upstream requests through the configured HTTP CONNECT or
// SOCKS5 proxy. The proxy address itself must not resolve to a private
// address unless hooks.AllowPrivateProxy is set.
func customClient(client *http.Client, proxy *repository.EgressProxy, upstream *url.URL, hooks Hooks) (*http.Client, error) {
	if err := checkProxyAddress(proxy, hooks); err != nil {
		return nil, err
	}
	password, err := DecryptPassword(proxy.Password)
	if err != nil {
		return nil, fmt.Errorf("decrypt egress proxy credentials: %w", err)
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	base, ok := transport.(*http.Transport)
	if !ok {
		return nil, errors.New("egress HTTP client must use *http.Transport")
	}
	copy := *client
	switch proxy.Protocol {
	case repository.EgressProxyProtocolHTTP:
		proxyURL := &url.URL{Scheme: "http", Host: net.JoinHostPort(proxy.Host, fmt.Sprint(proxy.Port))}
		if proxy.Username != "" {
			proxyURL.User = url.UserPassword(proxy.Username, password)
		}
		custom := base.Clone()
		custom.Proxy = http.ProxyURL(proxyURL)
		// The proxy establishes the upstream connection (CONNECT); a local
		// dial hook would bypass it.
		custom.DialContext = nil
		custom.Dial = nil //nolint:staticcheck
		copy.Transport = custom
		return &copy, nil
	case repository.EgressProxyProtocolSOCKS5:
		var auth *xproxy.Auth
		if proxy.Username != "" {
			auth = &xproxy.Auth{User: proxy.Username, Password: password}
		}
		forward := hookDialer{hooks: hooks}
		dialer, err := xproxy.SOCKS5("tcp", net.JoinHostPort(proxy.Host, fmt.Sprint(proxy.Port)), auth, forward)
		if err != nil {
			return nil, fmt.Errorf("create socks5 dialer: %w", err)
		}
		contextDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, errors.New("socks5 dialer does not support contexts")
		}
		custom := base.Clone()
		custom.Proxy = nil
		if proxy.RemoteDNS {
			// socks5h 语义：主机名交给代理解析，适用于本地 DNS 不可达上游的网络。
			custom.DialContext = contextDialer.DialContext
		} else {
			custom.DialContext = socksLocalResolveDialer(upstream, hooks, contextDialer)
		}
		copy.Transport = custom
		return &copy, nil
	default:
		return nil, fmt.Errorf("unsupported egress proxy protocol %q", proxy.Protocol)
	}
}

// hookDialer adapts Hooks.DialContext to x/net/proxy.Dialer so the SOCKS5
// dialer's own network hop remains injectable in tests.
type hookDialer struct{ hooks Hooks }

func (d hookDialer) Dial(network, address string) (net.Conn, error) {
	return d.hooks.DialContext(context.Background(), network, address)
}

// socksLocalResolveDialer resolves the upstream hostname locally (with the
// private-address check) and hands the SOCKS5 proxy an IP literal, preserving
// the SSRF posture of direct mode.
func socksLocalResolveDialer(upstream *url.URL, hooks Hooks, dialer xproxy.ContextDialer) func(ctx context.Context, network, address string) (net.Conn, error) {
	hostname := upstream.Hostname()
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		requestHost, requestPort, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(requestHost, hostname) {
			return nil, errors.New("egress dial target changed")
		}
		ips, err := hooks.LookupIP(ctx, "ip", requestHost)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("resolve egress endpoint: %w", err)
		}
		var lastErr error
		for _, ip := range ips {
			if PrivateAddress(ip) {
				return nil, errors.New("egress endpoint resolves to a private address")
			}
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), requestPort))
			if err == nil {
				return connection, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

// checkProxyAddress resolves the configured proxy host and rejects private or
// loopback results unless the hooks explicitly allow them.
func checkProxyAddress(proxy *repository.EgressProxy, hooks Hooks) error {
	ips, err := hooks.LookupIP(context.Background(), "ip", proxy.Host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("resolve egress proxy address: %w", err)
	}
	if hooks.AllowPrivateProxy {
		return nil
	}
	for _, ip := range ips {
		if PrivateAddress(ip) {
			return errors.New("egress proxy address resolves to a private address")
		}
	}
	return nil
}

// bypassedByNoProxy reports whether the upstream host matches a noProxy
// suffix or CIDR entry. Matching targets the upstream host, never the proxy.
func bypassedByNoProxy(host string, noProxy []string) bool {
	for _, entry := range noProxy {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
				return true
			}
			continue
		}
		suffix := strings.TrimPrefix(entry, "*.")
		if strings.EqualFold(host, suffix) || strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(suffix)) {
			return true
		}
	}
	return false
}

// HasTLSOverride reports whether the client overrides TLS dialing, which would
// bypass both the pinned connection path and any configured egress proxy.
func HasTLSOverride(client *http.Client) bool {
	if client == nil {
		return false
	}
	base, ok := client.Transport.(*http.Transport)
	return ok && base != nil && (base.DialTLSContext != nil || hasLegacyDialTLS(base))
}

// DialTLS is deprecated but an installed legacy hook must still be rejected:
// it would bypass the pinned connection path.
func hasLegacyDialTLS(transport *http.Transport) bool {
	field := reflect.ValueOf(transport).Elem().FieldByName("DialTLS")
	return field.IsValid() && !field.IsNil()
}

// PrivateAddress reports whether ip is loopback, private, link-local,
// unspecified, or multicast.
func PrivateAddress(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()
}
