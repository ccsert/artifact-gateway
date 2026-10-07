package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const proxyMaxRedirects = 5

func (c UpstreamClient) upstreamEgressHooks() egress.Hooks {
	if c.EgressHooks != nil {
		hooks := *c.EgressHooks
		defaults := rawEgressHooks()
		if hooks.LookupIP == nil {
			hooks.LookupIP = defaults.LookupIP
		}
		if hooks.DialContext == nil {
			hooks.DialContext = defaults.DialContext
		}
		if hooks.ProxyFromEnvironment == nil {
			hooks.ProxyFromEnvironment = defaults.ProxyFromEnvironment
		}
		return hooks
	}
	return rawEgressHooks()
}

// doProxyUpstream applies one policy decision and a new pinned/proxied
// transport to each hop. A single context deadline covers DNS, redirects and
// consumption of the final response; closing its body releases that budget.
func (c UpstreamClient) doProxyUpstream(ctx context.Context, member repository.Member, request *http.Request) (*http.Response, error) {
	base := c.HTTPClient
	if base == nil {
		base = &http.Client{Timeout: 30 * time.Second}
	}
	timeout := base.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	keepBody := false
	defer func() {
		if !keepBody {
			cancel()
		}
	}()
	endpoint, err := url.Parse(member.Endpoint)
	if err != nil || !proxyURLValid(endpoint, c.AllowHTTPForTesting) || request == nil || !proxyTargetAllowed(member, request.URL, c.AllowHTTPForTesting) {
		return nil, errors.New("proxy upstream target is not allowed")
	}
	current := request.Clone(ctx)
	visited := make(map[string]bool, proxyMaxRedirects+1)
	history := make([]*http.Request, 0, proxyMaxRedirects+1)
	for redirects := 0; ; redirects++ {
		if !proxyTargetAllowed(member, current.URL, c.AllowHTTPForTesting) {
			return nil, errors.New("proxy upstream target is not allowed")
		}
		key := proxyOrigin(current.URL) + current.URL.RequestURI()
		if visited[key] {
			return nil, errors.New("proxy upstream redirect loop")
		}
		visited[key] = true
		history = append(history, current)
		client := *base
		client.Timeout = 0 // The shared context owns the complete chain's budget.
		client.Jar = nil // Upstream credentials come only from the bound request.
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		var release func()
		if current.URL.Scheme == "https" {
			hooks := c.upstreamEgressHooks()
			lookup := hooks.LookupIP
			hooks.LookupIP = func(ctx context.Context, network, host string) ([]net.IP, error) {
				ips, err := lookup(ctx, network, host)
				if err != nil {
					return nil, err
				}
				for _, ip := range ips {
					if proxyReservedAddress(ip) {
						return nil, errors.New("proxy upstream address is not allowed")
					}
				}
				return ips, nil
			}
			applied, applyErr := egress.ApplyContext(ctx, &client, member.EgressProxy, current.URL.String(), hooks)
			if applyErr != nil {
				return nil, proxyUpstreamError(ctx, "proxy upstream egress unavailable", applyErr)
			}
			client = *applied
			release = applied.CloseIdleConnections
		} else {
			// This is an explicit in-process fixture seam. It is unavailable via
			// stored configuration and cannot enable a TLS downgrade.
			release = func() {}
		}
		response, fetchErr := doProxyHTTP(&client, current)
		if fetchErr != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			release()
			return nil, proxyUpstreamError(ctx, "proxy upstream unavailable", fetchErr)
		}
		if !proxyRedirectStatus(response.StatusCode) {
			response.Body = &proxyResponseBody{ReadCloser: response.Body, cancel: cancel, release: release}
			keepBody = true
			return response, nil
		}
		location := response.Header.Get("Location")
		if location == "" {
			_ = response.Body.Close()
			release()
			return nil, errors.New("proxy upstream redirect has no location")
		}
		nextURL, parseErr := current.URL.Parse(location)
		if redirects >= proxyMaxRedirects {
			_ = response.Body.Close()
			release()
			return nil, errors.New("proxy upstream redirect limit exceeded")
		}
		if parseErr != nil || !proxyTargetAllowed(member, nextURL, c.AllowHTTPForTesting) {
			_ = response.Body.Close()
			release()
			return nil, errors.New("proxy upstream redirect is not allowed")
		}
		next := current.Clone(ctx)
		next.URL = nextURL
		next.Host = ""
		if proxyOrigin(nextURL) != proxyOrigin(current.URL) {
			next.Header.Del("Authorization")
			next.Header.Del("Cookie")
			next.Header.Del("Proxy-Authorization")
		}
		// Preserve an explicitly installed additional redirect restriction.
		if base.CheckRedirect != nil {
			if err := base.CheckRedirect(next, history); err != nil {
				if err == http.ErrUseLastResponse {
					response.Body = &proxyResponseBody{ReadCloser: response.Body, cancel: cancel, release: release}
					keepBody = true
					return response, nil
				}
				_ = response.Body.Close()
				release()
				return nil, &proxyFetchError{message: "proxy upstream redirect rejected by client", cause: err}
			}
		}
		_ = response.Body.Close()
		release()
		if proxyOrigin(next.URL) != proxyOrigin(current.URL) {
			next.Header.Del("Authorization")
			next.Header.Del("Cookie")
			next.Header.Del("Proxy-Authorization")
		}
		current = next
	}
}

func proxyRedirectStatus(status int) bool {
	return status == 301 || status == 302 || status == 303 || status == 307 || status == 308
}

func proxyURLValid(u *url.URL, allowHTTP bool) bool {
	if u == nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if u.Scheme != "https" && (!allowHTTP || u.Scheme != "http") {
		return false
	}
	if strings.ContainsAny(u.Hostname(), "\\% \t\r\n") || strings.HasSuffix(u.Hostname(), ".") {
		return false
	}
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && proxyReservedAddress(ip) && (!allowHTTP || u.Scheme != "http") {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return true
}

func proxyOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func proxyTargetAllowed(member repository.Member, target *url.URL, allowHTTP bool) bool {
	endpoint, err := url.Parse(member.Endpoint)
	if err != nil || !proxyURLValid(endpoint, allowHTTP) || !proxyURLValid(target, allowHTTP) || target.Scheme != endpoint.Scheme {
		return false
	}
	if proxyOrigin(target) == proxyOrigin(endpoint) {
		return true
	}
	for _, allowed := range member.AllowedHosts {
		candidate, err := url.Parse(endpoint.Scheme + "://" + strings.TrimSpace(allowed))
		if err == nil && proxyURLValid(candidate, allowHTTP) && candidate.Path == "" && candidate.RawQuery == "" && proxyOrigin(target) == proxyOrigin(candidate) {
			return true
		}
	}
	return false
}

// These special-use ranges are denied on locally resolved paths. Remote DNS
// remains the selected egress proxy's responsibility; URL policy still applies.
var proxyReservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"), netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
}

func proxyReservedAddress(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok || egress.PrivateAddress(ip) {
		return true
	}
	address = address.Unmap()
	if address.Is6() && !netip.MustParsePrefix("2000::/3").Contains(address) {
		return true
	}
	for _, prefix := range proxyReservedPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func proxyUpstreamError(ctx context.Context, message string, err error) error {
	if ctx.Err() != nil {
		return errors.Join(errors.New(message), ctx.Err())
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return errors.Join(errors.New(message), context.DeadlineExceeded)
	}
	return &proxyFetchError{message: message, cause: err}
}

type proxyFetchError struct {
	message string
	cause   error
}

func (e *proxyFetchError) Error() string { return e.message }
func (e *proxyFetchError) Unwrap() error { return e.cause }

// The generic HTTP instrumentation records URL/query and raw transport errors.
// Proxy redirects may carry signed download queries, so use a safe span that
// preserves trace propagation without recording targets or original messages.
func doProxyHTTP(client *http.Client, request *http.Request) (*http.Response, error) {
	ctx, span := otel.Tracer("artifact-gateway.proxy").Start(request.Context(), "proxy.upstream", trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()
	request = request.Clone(ctx)
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))
	response, err := client.Do(request)
	if err != nil {
		span.RecordError(proxyUpstreamError(ctx, "proxy upstream unavailable", err))
		span.SetStatus(codes.Error, "proxy upstream unavailable")
	}
	return response, err
}

type proxyResponseBody struct {
	io.ReadCloser
	cancel  context.CancelFunc
	release func()
	once    sync.Once
}

func (b *proxyResponseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.cancel(); b.release() })
	return err
}
