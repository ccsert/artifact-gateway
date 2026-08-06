package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/egress"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// egressProxyRequest is the write shape for per-repository egress proxy
// configuration. Password arrives as plaintext over TLS and is encrypted
// before storage; it never appears in responses.
type egressProxyRequest struct {
	Mode             repository.EgressProxyMode     `json:"mode"`
	Protocol         repository.EgressProxyProtocol `json:"protocol,omitempty"`
	Host             string                         `json:"host,omitempty"`
	Port             int                            `json:"port,omitempty"`
	Username         string                         `json:"username,omitempty"`
	Password         string                         `json:"password,omitempty"`
	ClearCredentials bool                           `json:"clearCredentials,omitempty"`
	RemoteDNS        bool                           `json:"remoteDns,omitempty"`
	NoProxy          []string                       `json:"noProxy,omitempty"`
}

// resolveEgressProxy validates the request shape and encrypts credentials.
// existing carries the currently stored configuration on update (nil on
// create); its credentials survive unless replaced or explicitly cleared.
func resolveEgressProxy(request *egressProxyRequest, existing *repository.EgressProxy) (*repository.EgressProxy, error) {
	if request == nil {
		return existing, nil
	}
	resolved := &repository.EgressProxy{
		Mode:      request.Mode,
		Protocol:  request.Protocol,
		Host:      request.Host,
		Port:      request.Port,
		Username:  request.Username,
		RemoteDNS: request.RemoteDNS,
		NoProxy:   request.NoProxy,
	}
	switch {
	case request.Password != "":
		encrypted, err := egress.EncryptPassword(request.Password)
		if err != nil {
			return nil, err
		}
		resolved.Password = encrypted
	case request.ClearCredentials:
		resolved.Password = ""
	case resolved.Mode == repository.EgressProxyModeCustom && existing != nil && existing.Mode == repository.EgressProxyModeCustom:
		resolved.Password = existing.Password
	}
	if err := resolved.Validate(); err != nil {
		return nil, err
	}
	return resolved, nil
}

// redactEgressProxy strips stored credentials from a repository about to be
// encoded in a management response, replacing them with a configured marker.
func redactEgressProxy(repo repository.HostedRepository) repository.HostedRepository {
	if repo.EgressProxy == nil {
		return repo
	}
	redacted := *repo.EgressProxy
	redacted.CredentialsConfigured = redacted.Password != ""
	redacted.Password = ""
	repo.EgressProxy = &redacted
	return repo
}

// egressProxyTestResult is the ephemeral response of the :test endpoint.
type egressProxyTestResult struct {
	Reachable      bool      `json:"reachable"`
	EgressMode     string    `json:"egressMode"`
	ProxyHost      string    `json:"proxyHost,omitempty"`
	UpstreamStatus int       `json:"upstreamStatus,omitempty"`
	LatencyMs      int64     `json:"latencyMs,omitempty"`
	Error          string    `json:"error,omitempty"`
	CheckedAt      time.Time `json:"checkedAt"`
}

// TestEgressProxy probes the repository's egress path to its upstream with the
// stored configuration. Administrator-only; nothing is persisted.
func (h generatedRepositoryAPIAdapter) TestEgressProxy(w http.ResponseWriter, r *http.Request, id adminopenapi.RepositoryId) {
	if _, ok := h.authorize(w, r); !ok {
		return
	}
	repo, err := h.store.GetHostedRepository(r.Context(), id.String())
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return
	}
	if repo.Type != repository.RepositoryTypeProxy {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "egress proxy tests only apply to proxy repositories")
		return
	}
	result := probeEgressProxy(r.Context(), repo)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

// probeEgressProxy builds the egress client exactly as the protocol paths do
// and issues a lightweight HEAD probe against the upstream endpoint. Error
// text is sanitized so proxy credentials can never leak through URL-bearing
// error messages.
func probeEgressProxy(ctx context.Context, repo repository.HostedRepository) egressProxyTestResult {
	mode := repository.EgressProxyModeEnvironment
	if repo.EgressProxy != nil && repo.EgressProxy.Mode != "" {
		mode = repo.EgressProxy.Mode
	}
	result := egressProxyTestResult{EgressMode: string(mode), CheckedAt: time.Now().UTC()}
	if mode == repository.EgressProxyModeCustom {
		result.ProxyHost = repo.EgressProxy.Host
	}
	client, err := egress.Apply(&http.Client{Timeout: 10 * time.Second}, repo.EgressProxy, repo.Endpoint, rawEgressHooks())
	if err != nil {
		switch {
		case errors.Is(err, egress.ErrKeyNotConfigured):
			result.Error = "egress proxy encryption key is not configured"
		default:
			result.Error = sanitizeEgressError(err)
		}
		return result
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, repo.Endpoint, nil)
	if err != nil {
		result.Error = "upstream endpoint is invalid"
		return result
	}
	started := time.Now()
	response, err := client.Do(request)
	result.LatencyMs = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = sanitizeEgressError(err)
		return result
	}
	defer func() { _ = response.Body.Close() }()
	result.Reachable = true
	result.UpstreamStatus = response.StatusCode
	return result
}

// sanitizeEgressError maps low-level failures to short, credential-free
// categories.
func sanitizeEgressError(err error) string {
	text := err.Error()
	switch {
	case strings.Contains(text, "private address"):
		return "egress address resolves to a private address"
	case strings.Contains(text, "must not override TLS dialing"):
		return "egress client TLS configuration is invalid"
	case strings.Contains(text, "cannot be decrypted"):
		return "stored credentials cannot be decrypted with the configured key"
	case strings.Contains(text, "resolve egress proxy address"):
		return "egress proxy address cannot be resolved"
	case strings.Contains(text, "resolve egress endpoint"):
		return "upstream endpoint cannot be resolved"
	default:
		return "connection failed"
	}
}
