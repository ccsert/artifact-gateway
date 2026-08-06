package app

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type mavenProxyOperationsHandler struct {
	store         repository.HostedRepositoryStore
	authenticator Authenticator
	authorizer    RepositoryAuthorizer
	client        MavenClient
	cache         *MavenCache
	maintenance   *CacheMaintenance
}

type mavenCacheRefreshRequest struct {
	Path       string `json:"path"`
	GAV        string `json:"gav"`
	Coordinate string `json:"coordinate"`
}

type mavenCacheRefreshResponse struct {
	RepositoryID string `json:"repositoryId"`
	Repository   string `json:"repository"`
	Path         string `json:"path"`
	Status       int    `json:"status"`
	Refreshed    bool   `json:"refreshed"`
	CacheKey     string `json:"cacheKey,omitempty"`
	ContentType  string `json:"contentType,omitempty"`
	Size         int64  `json:"size,omitempty"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
}

type mavenProxyHealthResponse struct {
	RepositoryID string                 `json:"repositoryId"`
	Repository   string                 `json:"repository"`
	Endpoint     string                 `json:"endpoint"`
	Reachable    bool                   `json:"reachable"`
	Status       int                    `json:"status,omitempty"`
	Error        string                 `json:"error,omitempty"`
	ProxyAllowed bool                   `json:"proxyAllowed"`
	CircuitOpen  bool                   `json:"circuitOpen"`
	CacheEnabled bool                   `json:"cacheEnabled"`
	CacheStatus  CacheMaintenanceStatus `json:"cacheStatus,omitempty"`
	CheckedAt    time.Time              `json:"checkedAt"`
}

func (h mavenProxyOperationsHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	_, repo, ok := h.withMavenProxyRepository(w, r, RepositoryAdmin)
	if !ok {
		return
	}
	if h.cache == nil || h.client == nil {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "Maven proxy cache is not enabled")
		return
	}
	var request mavenCacheRefreshRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	path, err := mavenRefreshPath(request)
	if err != nil {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	member := repository.Member{Type: repository.MemberProxy, Name: repo.Name, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, EgressProxy: repo.EgressProxy}
	if !h.cache.ProxyAllowed(member.Endpoint) {
		writeHostedProblem(w, http.StatusForbidden, "proxy_denied", "upstream repository is not allowed")
		return
	}
	if !h.cache.UpstreamAllowed(r.Context(), member.Endpoint) {
		writeHostedProblem(w, http.StatusServiceUnavailable, "circuit_open", "upstream circuit is open")
		return
	}
	response, err := h.fetchMavenWithRetry(r, member, path)
	if err != nil {
		h.cache.RecordUpstreamFailure(r.Context(), member.Endpoint)
		writeHostedProblem(w, http.StatusBadGateway, "upstream_error", "upstream fetch failed")
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if retryableMavenStatus(response.StatusCode) {
			h.cache.RecordUpstreamFailure(r.Context(), member.Endpoint)
		}
		writeNativeMavenJSON(w, http.StatusBadGateway, mavenCacheRefreshResponse{RepositoryID: repo.ID, Repository: repo.Name, Path: path, Status: response.StatusCode, Refreshed: false})
		return
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		h.cache.RecordUpstreamFailure(r.Context(), member.Endpoint)
		writeHostedProblem(w, http.StatusBadGateway, "upstream_error", "upstream response could not be read")
		return
	}
	cacheKey := h.cache.Key(repo.Name, path)
	content := CachedMavenContent{Body: body, ContentType: response.Header.Get("Content-Type"), ETag: response.Header.Get("ETag"), LastModified: response.Header.Get("Last-Modified"), Member: repo.Name, Endpoint: repo.Endpoint, Repository: repo.Name}
	if err := h.cache.Store(r.Context(), cacheKey, path, content); err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "cache_error", "unable to store refreshed Maven content")
		return
	}
	h.cache.RecordUpstreamSuccess(r.Context(), member.Endpoint)
	writeNativeMavenJSON(w, http.StatusOK, mavenCacheRefreshResponse{RepositoryID: repo.ID, Repository: repo.Name, Path: path, Status: response.StatusCode, Refreshed: true, CacheKey: cacheKey, ContentType: content.ContentType, Size: int64(len(body)), ETag: content.ETag, LastModified: content.LastModified})
}

func (h mavenProxyOperationsHandler) Health(w http.ResponseWriter, r *http.Request) {
	_, repo, ok := h.withMavenProxyRepository(w, r, RepositoryRead)
	if !ok {
		return
	}
	member := repository.Member{Type: repository.MemberProxy, Name: repo.Name, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts, EgressProxy: repo.EgressProxy}
	result := mavenProxyHealthResponse{RepositoryID: repo.ID, Repository: repo.Name, Endpoint: repo.Endpoint, CacheEnabled: h.cache != nil, CheckedAt: time.Now().UTC()}
	if h.cache != nil {
		result.ProxyAllowed = h.cache.ProxyAllowed(member.Endpoint)
		result.CircuitOpen = !h.cache.UpstreamAllowed(r.Context(), member.Endpoint)
	}
	if h.maintenance != nil {
		status, err := h.maintenance.Status(r.Context())
		if err != nil {
			writeHostedProblem(w, http.StatusInternalServerError, "storage_error", "unable to inspect cache")
			return
		}
		result.CacheStatus = status
	}
	if h.client == nil {
		result.Error = "Maven upstream client is not configured"
		writeNativeMavenJSON(w, http.StatusOK, result)
		return
	}
	response, err := h.client.FetchMaven(r.Context(), http.MethodHead, member, "", http.Header{})
	if err != nil {
		result.Error = "upstream health check failed"
		writeNativeMavenJSON(w, http.StatusOK, result)
		return
	}
	defer func() { _ = response.Body.Close() }()
	result.Status = response.StatusCode
	result.Reachable = response.StatusCode < http.StatusInternalServerError
	if result.Reachable && h.cache != nil {
		h.cache.RecordUpstreamSuccess(r.Context(), member.Endpoint)
	}
	writeNativeMavenJSON(w, http.StatusOK, result)
}

func (h mavenProxyOperationsHandler) withMavenProxyRepository(w http.ResponseWriter, r *http.Request, operation RepositoryOperation) (Principal, repository.HostedRepository, bool) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeHostedProblem(w, http.StatusUnauthorized, "access_denied", "authentication is required")
		return Principal{}, repository.HostedRepository{}, false
	}
	repo, err := h.store.GetHostedRepository(r.Context(), r.PathValue("repositoryId"))
	if errors.Is(err, repository.ErrNotFound) {
		writeHostedProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return Principal{}, repository.HostedRepository{}, false
	}
	if err != nil {
		writeHostedProblem(w, http.StatusInternalServerError, "internal_error", "get repository failed")
		return Principal{}, repository.HostedRepository{}, false
	}
	if repo.Type != repository.RepositoryTypeProxy || repo.Format != repository.FormatMaven || repo.State != repository.RepositoryActive {
		writeHostedProblem(w, http.StatusBadRequest, "invalid_repository", "operation is only available for active Maven proxy repositories")
		return Principal{}, repository.HostedRepository{}, false
	}
	if decision := h.authorizer.Authorize(r.Context(), principal, repo, operation); !decision.Allowed {
		writeHostedProblem(w, http.StatusForbidden, "access_denied", "repository scope is required")
		return Principal{}, repository.HostedRepository{}, false
	}
	return principal, repo, true
}

func (h mavenProxyOperationsHandler) fetchMavenWithRetry(r *http.Request, member repository.Member, path string) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		response, err := h.client.FetchMaven(r.Context(), http.MethodGet, member, path, http.Header{})
		if err != nil {
			lastErr = err
			continue
		}
		if !retryableMavenStatus(response.StatusCode) || attempt == 1 {
			return response, nil
		}
		_ = response.Body.Close()
	}
	return nil, lastErr
}

func mavenRefreshPath(request mavenCacheRefreshRequest) (string, error) {
	path := strings.TrimSpace(request.Path)
	coordinate := strings.TrimSpace(request.GAV)
	if coordinate == "" {
		coordinate = strings.TrimSpace(request.Coordinate)
	}
	if (path == "") == (coordinate == "") {
		return "", errors.New("provide exactly one of path or gav")
	}
	if coordinate != "" {
		if !validMavenCoordinate(coordinate) {
			return "", errors.New("gav must be a valid Maven group:artifact:version coordinate")
		}
		parts := strings.Split(coordinate, ":")
		base := mavenCoordinatePath(coordinate)
		return base + "/" + parts[1] + "-" + parts[2] + ".pom", nil
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return "", errors.New("path is required")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", errors.New("path must be a normalized Maven artifact path")
		}
	}
	return path, nil
}
