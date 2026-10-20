package raw

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ccsert/artifact-gateway/internal/egress"
	"github.com/ccsert/artifact-gateway/internal/operationalog"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/upstreamauth"
)

// Upstream owns Raw URL assembly and outbound policy. Hooks and tracing are
// explicit transport seams, shared with the composition root's other clients.
type Upstream struct {
	HTTPClient *http.Client
	Hooks      egress.Hooks
	Trace      func(*http.Client) *http.Client
}

func (c Upstream) trace(client *http.Client) *http.Client {
	if c.Trace != nil {
		return c.Trace(client)
	}
	if client == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	copy := *client
	return &copy
}
func (c Upstream) FetchRaw(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	client := c.HTTPClient
	if member.Type == repository.MemberProxy {
		var err error
		client, err = egress.Apply(client, member.EgressProxy, member.Endpoint, c.Hooks)
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
	if err := upstreamauth.ApplyOrigin(r, member.Endpoint, member.UpstreamAuth); err != nil {
		return nil, &rawFetchError{code: "upstream_status_rejected", err: err}
	}
	// Raw has one cached file representation. Range and content negotiation are
	// applied locally after the complete canonical representation is fetched.
	client = c.trace(client)
	// Never follow upstream redirects: a redirect can otherwise bypass the
	// configured proxy host allowlist (and may disclose hosted credentials).
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(r)
	if err != nil {
		return response, &rawFetchError{code: "upstream_transport_failed", err: err}
	}
	return response, nil
}

type rawFetchError struct {
	code string
	err  error
}

func (e *rawFetchError) Error() string { return e.err.Error() }
func (e *rawFetchError) Unwrap() error { return e.err }
func FetchFailureCode(err error) string {
	var failure *rawFetchError
	if errors.As(err, &failure) && operationalog.FailurePhase(failure.code) != "" {
		return failure.code
	}
	return "unknown"
}
