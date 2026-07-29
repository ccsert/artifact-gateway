package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type MavenClient interface {
	FetchMaven(context.Context, string, repository.Member, string, http.Header) (*http.Response, error)
}

func (c UpstreamClient) FetchMaven(ctx context.Context, method string, member repository.Member, artifactPath string, headers http.Header) (*http.Response, error) {
	endpoint, err := url.Parse(member.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse Maven endpoint: %w", err)
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + artifactPath
	endpoint.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Maven request: %w", err)
	}
	for _, name := range []string{"Accept", "If-Modified-Since", "If-None-Match", "Range"} {
		if value := headers.Get(name); value != "" {
			request.Header.Set(name, value)
		}
	}
	response, err := tracedHTTPClient(c.HTTPClient).Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Maven content: %w", err)
	}
	return response, nil
}

type MavenHandler struct {
	Store         repository.MavenStore
	Repositories  repository.HostedRepositoryStore
	Authorizer    RepositoryAuthorizer
	Authenticator Authenticator
	Client        MavenClient
	Metrics       *Metrics
	Cache         *MavenCache
}

func (h MavenHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	groupName, artifactPath, ok := parseMavenPath(request.URL.Path)
	if !ok {
		http.NotFound(w, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		if _, authenticated := h.authenticate(request); !authenticated {
			if err := h.auditAnonymousDenied(request.Context(), groupName, artifactPath, request.Method, http.StatusMethodNotAllowed); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	principal, ok := h.authenticate(request)
	if !ok && h.anonymousMavenAllowed(request.Context(), groupName) {
		principal = Principal{Actor: "anonymous"}
		ok = true
	}
	if !ok {
		if err := h.auditAnonymousDenied(request.Context(), groupName, artifactPath, request.Method, http.StatusUnauthorized); err != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Maven"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	actor := principal.Actor
	if actor == "anonymous" {
		h.Metrics.recordAnonymousRead()
	}
	// A Maven Group is the repository boundary. Artifact paths are request
	// details, not distinct repositories for authorization or operations.
	repositoryName := groupName
	h.Metrics.recordRequest(repositoryName)
	// A native proxy repository claims the path before legacy Group resolution:
	// it is authorized through repository grants and served from its upstream.
	if h.serveNativeProxy(w, request, groupName, artifactPath, principal) {
		return
	}
	if principal.Actor != "anonymous" && !h.Authenticator.CanReadMavenRepository(principal, repositoryName) {
		if err := h.audit(request.Context(), groupName, artifactPath, "", actor, repository.AuditAccessDenied); err != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		h.Metrics.failed.Add(1)
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	group, err := h.Store.GetMavenGroup(request.Context(), groupName)
	if err != nil {
		if auditErr := h.audit(request.Context(), groupName, artifactPath, "", actor, auditOutcome(err)); auditErr != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		h.Metrics.failed.Add(1)
		if !errors.Is(err, repository.ErrNotFound) {
			http.Error(w, "unable to resolve Maven group", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, request)
		return
	}
	if !group.Enabled {
		if err := h.audit(request.Context(), groupName, artifactPath, "", actor, repository.AuditGroupDisabled); err != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		h.Metrics.failed.Add(1)
		http.Error(w, "Maven group is disabled", http.StatusForbidden)
		return
	}
	members := group.Members
	if actor == "anonymous" {
		members = make([]repository.Member, 0, len(group.Members))
		for _, member := range group.Members {
			if member.Anonymous {
				members = append(members, member)
			}
		}
	}
	members = prioritizeHosted(members)
	var hadAuthorizationDenied bool
	if actor != "anonymous" {
		members, hadAuthorizationDenied, err = h.authorizedMavenMembers(request.Context(), groupName, artifactPath, actor, principal, members)
		if err != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
	}
	if len(members) == 0 {
		if hadAuthorizationDenied {
			h.Metrics.failed.Add(1)
			http.Error(w, "repository read permission required", http.StatusForbidden)
			return
		}
		if err := h.audit(request.Context(), groupName, artifactPath, "", actor, repository.AuditNotFound); err != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		h.Metrics.failed.Add(1)
		http.NotFound(w, request)
		return
	}
	h.serveResolvedMembers(w, request, groupName, artifactPath, actor, members)
}

// serveResolvedMembers serves an artifact read against an ordered member set,
// applying the read-through cache, upstream fetch with retry, and negative
// caching. It is shared by legacy Groups and native proxy repositories, which
// pass a single synthetic proxy member.
func (h MavenHandler) serveResolvedMembers(w http.ResponseWriter, request *http.Request, groupName, artifactPath, actor string, members []repository.Member) {
	cacheKey := ""
	hasProxy := false
	hasHosted := false
	for _, member := range members {
		hasProxy = hasProxy || member.Type == repository.MemberProxy
		hasHosted = hasHosted || member.Type == repository.MemberHosted
	}
	if h.Cache != nil && hasProxy {
		cacheKey = h.Cache.Key(groupName, artifactPath)
		if !hasHosted {
			if h.serveMavenCache(w, request, groupName, artifactPath, actor, members, cacheKey) {
				return
			}
		}
	}
	hadFailure := false
	hadProxyDenied := false
	var notFoundProxy repository.Member
	for index, member := range members {
		if member.Type == repository.MemberProxy && h.Cache != nil {
			if hasHosted && h.serveMavenCache(w, request, groupName, artifactPath, actor, members, cacheKey) {
				return
			}
			if !h.Cache.ProxyAllowed(member.Endpoint) {
				h.Metrics.mavenProxyDenied.Add(1)
				hadProxyDenied = true
				if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditProxyDenied); err != nil {
					h.Metrics.failed.Add(1)
					http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
					return
				}
				continue
			}
			if !h.Cache.UpstreamAllowed(request.Context(), member.Endpoint) {
				h.Metrics.mavenCircuitOpen.Add(1)
				hadFailure = true
				continue
			}
			served := false
			locked, lockErr := h.Cache.WithRequestLock(request.Context(), cacheKey, func() error {
				served = h.serveMavenCache(w, request, groupName, artifactPath, actor, members, cacheKey)
				return nil
			})
			if lockErr != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to coordinate Maven cache fetch", http.StatusServiceUnavailable)
				return
			}
			if locked && served {
				return
			}
		}
		response, fetchErr := h.fetchMavenWithRetry(request.Context(), request.Method, member, artifactPath, request.Header)
		if fetchErr != nil {
			if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditUpstreamError); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
			hadFailure = true
			if member.Type == repository.MemberProxy && h.Cache != nil {
				h.Cache.RecordUpstreamFailure(request.Context(), member.Endpoint)
			}
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			_ = response.Body.Close()
			if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditNotFound); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
			if member.Type == repository.MemberProxy {
				notFoundProxy = member
			}
			continue
		}
		if response.StatusCode == http.StatusNotModified {
			defer func() { _ = response.Body.Close() }()
			if member.Type == repository.MemberHosted {
				if err := h.recordInternalPreference(request.Context(), groupName, artifactPath, members[index+1:], actor, w); err != nil {
					h.Metrics.failed.Add(1)
					http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
					return
				}
			}
			if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditResolved); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
			h.Metrics.resolved.Add(1)
			copyMavenHeaders(w.Header(), response.Header)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditUpstreamError); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
			hadFailure = true
			if member.Type == repository.MemberProxy && h.Cache != nil {
				h.Cache.RecordUpstreamFailure(request.Context(), member.Endpoint)
			}
			continue
		}
		if member.Type == repository.MemberProxy && h.Cache != nil {
			h.Cache.RecordUpstreamSuccess(request.Context(), member.Endpoint)
			if request.Method == http.MethodGet && request.Header.Get("Range") == "" {
				body, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if readErr != nil {
					hadFailure = true
					h.Cache.RecordUpstreamFailure(request.Context(), member.Endpoint)
					continue
				}
				content := CachedMavenContent{Body: body, ContentType: response.Header.Get("Content-Type"), ETag: response.Header.Get("ETag"), LastModified: response.Header.Get("Last-Modified"), Member: member.Name, Endpoint: member.Endpoint, Repository: groupName}
				if err := h.Cache.Store(request.Context(), cacheKey, artifactPath, content); err != nil {
					if errors.Is(err, ErrCacheQuotaExceeded) {
						h.Metrics.cacheQuotaDenied.Add(1)
						if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditResolved); err != nil {
							h.Metrics.failed.Add(1)
							http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
							return
						}
						h.Metrics.resolved.Add(1)
						serveCachedMavenContent(w, request, artifactPath, content)
						return
					}
					h.Metrics.failed.Add(1)
					http.Error(w, "unable to cache Maven content", http.StatusInternalServerError)
					return
				}
				if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditResolved); err != nil {
					h.Metrics.failed.Add(1)
					http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
					return
				}
				h.Metrics.resolved.Add(1)
				serveCachedMavenContent(w, request, artifactPath, content)
				return
			}
		}
		defer func() { _ = response.Body.Close() }()
		if member.Type == repository.MemberHosted {
			if err := h.recordInternalPreference(request.Context(), groupName, artifactPath, members[index+1:], actor, w); err != nil {
				h.Metrics.failed.Add(1)
				http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
				return
			}
		}
		if err := h.audit(request.Context(), groupName, artifactPath, member.Name, actor, repository.AuditResolved); err != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return
		}
		h.Metrics.resolved.Add(1)
		copyMavenHeaders(w.Header(), response.Header)
		w.WriteHeader(response.StatusCode)
		if request.Method != http.MethodHead {
			_, _ = io.Copy(w, response.Body)
		}
		return
	}
	h.Metrics.failed.Add(1)
	if hadProxyDenied {
		http.Error(w, "upstream repository is not allowed", http.StatusForbidden)
		return
	}
	if hadFailure {
		http.Error(w, "upstream repository unavailable", http.StatusBadGateway)
		return
	}
	if h.Cache != nil && notFoundProxy.Name != "" && !hadFailure && request.Method == http.MethodGet {
		_ = h.Cache.StoreNegative(request.Context(), cacheKey, notFoundProxy)
	}
	http.NotFound(w, request)
}

// serveNativeProxy claims the request when the first path segment is an active
// native Maven proxy repository. It authorizes through repository grants and
// serves the artifact from the repository's upstream via the shared proxy
// fetch and cache path, with the repository name as the cache/audit namespace.
func (h MavenHandler) serveNativeProxy(w http.ResponseWriter, request *http.Request, groupName, artifactPath string, principal Principal) bool {
	if h.Repositories == nil {
		return false
	}
	repo, err := h.Repositories.GetHostedRepositoryByName(request.Context(), groupName)
	if err != nil || repo.Format != repository.FormatMaven || repo.Type != repository.RepositoryTypeProxy || repo.State != repository.RepositoryActive {
		return false
	}
	resource := mavenResourceFromPath(artifactPath)
	if decision := h.Authorizer.AuthorizeResource(request.Context(), principal, repo, RepositoryRead, resource); !decision.Allowed {
		if auditErr := h.auditAuthorizationDenied(request.Context(), groupName, artifactPath, "", principal.Actor, decision); auditErr != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return true
		}
		h.Metrics.failed.Add(1)
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return true
	}
	member := repository.Member{Type: repository.MemberProxy, Name: repo.Name, Endpoint: repo.Endpoint, AllowedHosts: repo.AllowedHosts}
	h.serveResolvedMembers(w, request, repo.Name, artifactPath, principal.Actor, []repository.Member{member})
	return true
}

func (h MavenHandler) authorizedMavenMembers(ctx context.Context, groupName, artifactPath, actor string, principal Principal, members []repository.Member) ([]repository.Member, bool, error) {
	access := groupMemberAccess{Repositories: h.Repositories, Authorizer: h.Authorizer, Format: repository.FormatMaven}
	return access.filterManaged(ctx, principal, members, mavenResourceFromPath(artifactPath), func(member repository.Member, decision AuthorizationDecision) error {
		return h.auditAuthorizationDenied(ctx, groupName, artifactPath, member.Name, actor, decision)
	})
}

func (h MavenHandler) serveMavenCache(w http.ResponseWriter, request *http.Request, groupName, artifactPath, actor string, members []repository.Member, cacheKey string) bool {
	content, err := h.Cache.Load(request.Context(), cacheKey)
	if errors.Is(err, errMavenCacheNegative) {
		if !h.cacheSourceAllowed(content, members) {
			h.Cache.Invalidate(request.Context(), cacheKey)
			h.Metrics.mavenCacheMiss.Add(1)
			h.Metrics.recordCache(groupName, false)
			h.Metrics.mavenCacheInvalidated.Add(1)
			return false
		}
		if auditErr := h.audit(request.Context(), groupName, artifactPath, "", actor, repository.AuditNotFound); auditErr != nil {
			h.Metrics.failed.Add(1)
			http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
			return true
		}
		h.Metrics.mavenCacheHit.Add(1)
		h.Metrics.recordCache(groupName, true)
		h.Metrics.mavenNegativeHit.Add(1)
		h.Metrics.failed.Add(1)
		http.NotFound(w, request)
		return true
	}
	if err != nil {
		h.Metrics.mavenCacheMiss.Add(1)
		h.Metrics.recordCache(groupName, false)
		return false
	}
	if !h.cacheSourceAllowed(content, members) {
		h.Cache.Invalidate(request.Context(), cacheKey)
		h.Metrics.mavenCacheMiss.Add(1)
		h.Metrics.recordCache(groupName, false)
		h.Metrics.mavenCacheInvalidated.Add(1)
		h.Metrics.mavenProxyDenied.Add(1)
		return false
	}
	if err := h.audit(request.Context(), groupName, artifactPath, content.Member, actor, repository.AuditResolved); err != nil {
		h.Metrics.failed.Add(1)
		http.Error(w, "unable to record repository audit", http.StatusInternalServerError)
		return true
	}
	h.Metrics.mavenCacheHit.Add(1)
	h.Metrics.recordCache(groupName, true)
	h.Metrics.resolved.Add(1)
	serveCachedMavenContent(w, request, artifactPath, content)
	return true
}

func (h MavenHandler) cacheSourceAllowed(content CachedMavenContent, members []repository.Member) bool {
	return cacheSourceAllowed(content.Member, content.Endpoint, members, h.Cache.ProxyAllowed)
}

func (h MavenHandler) fetchMavenWithRetry(ctx context.Context, method string, member repository.Member, artifactPath string, headers http.Header) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		response, err := h.Client.FetchMaven(ctx, method, member, artifactPath, headers)
		if err != nil {
			lastErr = err
			if attempt == 0 {
				h.Metrics.mavenRetry.Add(1)
			}
			continue
		}
		if !retryableMavenStatus(response.StatusCode) || attempt == 1 {
			return response, nil
		}
		_ = response.Body.Close()
		h.Metrics.mavenRetry.Add(1)
	}
	return nil, lastErr
}

func retryableMavenStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func serveCachedMavenContent(w http.ResponseWriter, request *http.Request, artifactPath string, content CachedMavenContent) {
	if content.ContentType != "" {
		w.Header().Set("Content-Type", content.ContentType)
	}
	if content.ETag != "" {
		w.Header().Set("ETag", content.ETag)
	}
	if content.LastModified != "" {
		w.Header().Set("Last-Modified", content.LastModified)
	}
	if content.ETag != "" && request.Header.Get("If-None-Match") == content.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if lastModified, err := http.ParseTime(content.LastModified); err == nil {
		if modifiedSince, err := http.ParseTime(request.Header.Get("If-Modified-Since")); err == nil && !lastModified.After(modifiedSince) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	if request.Header.Get("Range") != "" && request.Method == http.MethodGet {
		http.ServeContent(w, request, artifactPath, time.Time{}, bytes.NewReader(content.Body))
		return
	}
	w.Header().Set("Content-Length", utoa(uint64(len(content.Body))))
	w.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = w.Write(content.Body)
	}
}

func (h MavenHandler) recordInternalPreference(ctx context.Context, groupName, artifactPath string, members []repository.Member, actor string, w http.ResponseWriter) error {
	conflictingMember, err := h.findConflict(ctx, groupName, members, artifactPath, actor)
	if err != nil {
		return err
	}
	if conflictingMember == "" {
		return nil
	}
	if err := h.audit(ctx, groupName, artifactPath, conflictingMember, actor, repository.AuditInternalPreferred); err != nil {
		return err
	}
	w.Header().Set("X-Artifact-Gateway-Conflict", "internal-preferred")
	return nil
}

func (h MavenHandler) findConflict(ctx context.Context, groupName string, members []repository.Member, artifactPath, actor string) (string, error) {
	probeContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, member := range members {
		response, err := h.Client.FetchMaven(probeContext, http.MethodHead, member, artifactPath, http.Header{})
		if err != nil {
			if auditErr := h.audit(ctx, groupName, artifactPath, member.Name, actor, repository.AuditUpstreamError); auditErr != nil {
				return "", auditErr
			}
			continue
		}
		_ = response.Body.Close()
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			return member.Name, nil
		}
		if response.StatusCode == http.StatusNotFound {
			if err := h.audit(ctx, groupName, artifactPath, member.Name, actor, repository.AuditNotFound); err != nil {
				return "", err
			}
			continue
		}
		if response.StatusCode != http.StatusMethodNotAllowed && response.StatusCode != http.StatusNotImplemented {
			if err := h.audit(ctx, groupName, artifactPath, member.Name, actor, repository.AuditUpstreamError); err != nil {
				return "", err
			}
			continue
		}
		response, err = h.Client.FetchMaven(probeContext, http.MethodGet, member, artifactPath, http.Header{})
		if err != nil {
			if auditErr := h.audit(ctx, groupName, artifactPath, member.Name, actor, repository.AuditUpstreamError); auditErr != nil {
				return "", auditErr
			}
			continue
		}
		_ = response.Body.Close()
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			return member.Name, nil
		}
		if response.StatusCode == http.StatusNotFound {
			if err := h.audit(ctx, groupName, artifactPath, member.Name, actor, repository.AuditNotFound); err != nil {
				return "", err
			}
			continue
		}
		if err := h.audit(ctx, groupName, artifactPath, member.Name, actor, repository.AuditUpstreamError); err != nil {
			return "", err
		}
	}
	return "", nil
}

func (h MavenHandler) authenticate(request *http.Request) (Principal, bool) {
	if principal, ok := h.Authenticator.Authenticate(request.Header.Get("Authorization")); ok {
		return principal, true
	}
	username, password, ok := request.BasicAuth()
	if !ok {
		return Principal{}, false
	}
	return h.Authenticator.AuthenticateBasic(username, password)
}

func (h MavenHandler) audit(ctx context.Context, groupName, artifactPath, memberName, actor string, outcome repository.AuditOutcome) error {
	if err := h.Store.RecordAudit(ctx, repository.AuditRecord{GroupName: groupName, Repository: groupName, MemberName: memberName, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC()}); err != nil {
		return err
	}
	h.Metrics.recordAudit(groupName, outcome)
	return nil
}

func (h MavenHandler) auditAuthorizationDenied(ctx context.Context, groupName, artifactPath, memberName, actor string, decision AuthorizationDecision) error {
	if err := h.Store.RecordAudit(ctx, repository.AuditRecord{
		GroupName: groupName, Repository: groupName, MemberName: memberName, Actor: actor, Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "maven", Resource: artifactPath, Representation: "body", Operation: strings.ToLower(http.MethodGet), Status: http.StatusForbidden, CacheDisposition: "bypass",
		AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason,
	}); err != nil {
		return err
	}
	h.Metrics.recordAudit(groupName, repository.AuditAccessDenied)
	h.Metrics.recordRepositoryAuthorizationDenied("maven", decision.Source, decision.Reason)
	return nil
}

func (h MavenHandler) auditAnonymousDenied(ctx context.Context, groupName, artifactPath, method string, status int) error {
	if err := h.Store.RecordAudit(ctx, repository.AuditRecord{
		GroupName: groupName, Repository: groupName, Actor: "anonymous", Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "maven", Resource: artifactPath, Representation: "body", Operation: strings.ToLower(method), Status: status, CacheDisposition: "bypass",
	}); err != nil {
		return err
	}
	h.Metrics.recordAudit(groupName, repository.AuditAccessDenied)
	h.Metrics.failed.Add(1)
	return nil
}

func parseMavenPath(path string) (groupName, artifactPath string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/maven/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		return "", "", false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", "", false
		}
	}
	return parts[0], strings.Join(parts[1:], "/"), true
}

func prioritizeHosted(members []repository.Member) []repository.Member {
	result := make([]repository.Member, 0, len(members))
	for _, member := range members {
		if member.Type == repository.MemberHosted {
			result = append(result, member)
		}
	}
	for _, member := range members {
		if member.Type == repository.MemberProxy {
			result = append(result, member)
		}
	}
	return result
}

func auditOutcome(err error) repository.AuditOutcome {
	if errors.Is(err, repository.ErrNotFound) {
		return repository.AuditNotFound
	}
	return repository.AuditStorageError
}

func copyMavenHeaders(destination, source http.Header) {
	for _, name := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified", "Cache-Control", "Content-Disposition"} {
		if value := source.Get(name); value != "" {
			destination.Set(name, value)
		}
	}
}
