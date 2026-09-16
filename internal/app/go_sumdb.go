package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// goSumDBResponseLimit bounds one mirrored checksum database response. Lookup
// notes and tiles are far smaller in practice; the bound only stops a
// misbehaving upstream from streaming unbounded bytes through the Gateway.
const goSumDBResponseLimit = 8 << 20

// goSumDBEgressHooks is a package variable so tests can pin resolution of the
// allowlisted checksum database host to a local fixture while the production
// hooks keep the standard private-address SSRF checks.
var goSumDBEgressHooks = func() egress.Hooks {
	return egress.Hooks{
		LookupIP:             net.DefaultResolver.LookupIP,
		DialContext:          (&net.Dialer{}).DialContext,
		ProxyFromEnvironment: http.ProxyFromEnvironment,
	}
}

// goSumDBEnabled reports whether the repository opted into checksum database
// mirroring at all. Mirroring egresses to the checksum database host, so
// Gateway keeps it fail closed: a Go Proxy repository mirrors exactly the
// hosts its egress allowlist names, and every other repository type never
// mirrors. A 404 on the go command's supported probe makes the client fall
// back to its direct checksum database connection.
func goSumDBEnabled(repo repository.HostedRepository) bool {
	return repo.Type == repository.RepositoryTypeProxy && len(repo.AllowedHosts) > 0
}

// goSumDBHost reduces a GOSUMDB name to the HTTP host that serves it. The go
// command accepts "sum.golang.org", "sum.golang.org+<public key>", and URL
// forms; only the host part reaches the network.
func goSumDBHost(name string) string {
	name = strings.TrimSpace(name)
	if parsed, err := url.Parse(name); err == nil && parsed.Scheme != "" {
		if parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return ""
		}
		return parsed.Host
	}
	if key := strings.Index(name, "+"); key >= 0 {
		name = name[:key]
	}
	if name == "" || strings.ContainsAny(name, "/?#") {
		return ""
	}
	parsed, err := url.Parse("//" + name)
	if err != nil || parsed.Host == "" || parsed.Host != name {
		return ""
	}
	return parsed.Host
}

// goSumDBTargetAllowed requires the checksum database host to be named by the
// repository's egress allowlist. Unlike the module upstream host, a checksum
// database is never implicitly trusted through the upstream endpoint. A bare
// allowlist entry permits any port; an entry that names a port requires an
// exact match.
func goSumDBTargetAllowed(repo repository.HostedRepository, host string) bool {
	target, parseErr := url.Parse("//" + host)
	if parseErr != nil || target.Hostname() == "" {
		return false
	}
	for _, allowed := range repo.AllowedHosts {
		allowedURL, parseErr := url.Parse("//" + strings.TrimSpace(allowed))
		if parseErr != nil || allowedURL.Hostname() == "" {
			continue
		}
		if !strings.EqualFold(allowedURL.Hostname(), target.Hostname()) {
			continue
		}
		if allowedURL.Port() == "" || strings.EqualFold(allowedURL.Port(), target.Port()) {
			return true
		}
	}
	return false
}

// FetchGoSumDB mirrors one GET against a checksum database host. Responses
// are signed tree notes that the go command verifies client-side, so the
// bytes, status codes, and content types are passed through verbatim and
// never cached. Only HTTPS targets named by the repository's egress allowlist
// are reachable, and the repository's upstream credential is never attached.
func (c UpstreamClient) FetchGoSumDB(ctx context.Context, repo repository.HostedRepository, target *url.URL) (*http.Response, error) {
	if target == nil || target.Scheme != "https" || target.Host == "" || target.User != nil ||
		!goSumDBTargetAllowed(repo, target.Host) {
		return nil, fmt.Errorf("go checksum database target is not allowed")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Go checksum database request: %w", err)
	}
	request.Header.Set("User-Agent", goProxyUserAgent)
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		shallowCopy := *client
		client = &shallowCopy
	}
	client, err = egress.Apply(client, repo.EgressProxy, target.String(), goSumDBEgressHooks())
	if err != nil {
		return nil, err
	}
	client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
		if next.URL.Scheme != "https" || next.URL.Host == "" || next.URL.User != nil ||
			!goSumDBTargetAllowed(repo, next.URL.Hostname()) {
			return fmt.Errorf("go checksum database redirect is not allowed")
		}
		return nil
	}
	response, err := tracedHTTPClient(client).Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Go checksum database content: %w", err)
	}
	return response, nil
}

// serveSumDB answers the go command's checksum database probe and mirrors one
// checksum database request for a Go Proxy repository. Repository reads are
// authorized by the caller; the mirrored content itself belongs to the
// checksum database, not to the repository.
func (h nativeGoHandler) serveSumDB(w http.ResponseWriter, r *http.Request, repo repository.HostedRepository, route goRoute) {
	if !goSumDBEnabled(repo) {
		http.NotFound(w, r)
		return
	}
	host := goSumDBHost(route.sumdbName)
	if host == "" || !goSumDBTargetAllowed(repo, host) {
		// An unknown checksum database name stays a 404 so the go command
		// falls back to its direct connection instead of failing the build.
		http.NotFound(w, r)
		return
	}
	if route.sumdbPath == "supported" {
		w.WriteHeader(http.StatusOK)
		return
	}
	target := &url.URL{Scheme: "https", Host: host, Path: "/" + route.sumdbPath}
	response, err := h.proxy.FetchGoSumDB(r.Context(), repo, target)
	if err != nil {
		http.Error(w, "checksum database is unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, goSumDBResponseLimit+1))
	if err != nil {
		http.Error(w, "checksum database is unavailable", http.StatusBadGateway)
		return
	}
	if len(payload) > goSumDBResponseLimit {
		http.Error(w, "checksum database response is too large", http.StatusBadGateway)
		return
	}
	for _, header := range []string{"Content-Type", "Content-Length"} {
		if value := response.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(payload)
}
