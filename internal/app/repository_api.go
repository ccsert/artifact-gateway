package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/v2contract"
)

type Adapter interface {
	Available(context.Context, repository.Member, string) bool
}

// openAPIServeMux bridges OpenAPI's `{sessionId}:commit` template to the
// standard library router, whose wildcard path segment must end with `}`.
type openAPIServeMux struct {
	mux       *http.ServeMux
	authorize func(http.ResponseWriter, *http.Request) (Principal, bool)
}

func (m openAPIServeMux) guarded(handler func(http.ResponseWriter, *http.Request)) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.authorize != nil {
			if _, ok := m.authorize(w, r); !ok {
				return
			}
		}
		handler(w, r)
	}
}

func (m openAPIServeMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	const commitPatternSuffix = "/publish-sessions/{sessionId}:commit"
	if strings.HasSuffix(pattern, commitPatternSuffix) {
		prefix := strings.TrimSuffix(pattern, "{sessionId}:commit")
		m.mux.HandleFunc(prefix, m.guarded(func(w http.ResponseWriter, r *http.Request) {
			sessionID := strings.TrimPrefix(r.URL.Path, strings.TrimPrefix(prefix, http.MethodPost+" "))
			if sessionID == "" || strings.Contains(sessionID, "/") || !strings.HasSuffix(sessionID, ":commit") {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			r.SetPathValue("sessionId", strings.TrimSuffix(sessionID, ":commit"))
			handler(w, r)
		}))
		return
	}
	m.mux.HandleFunc(pattern, m.guarded(handler))
}

func (m openAPIServeMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mux.ServeHTTP(w, r)
}

type TestAdapter struct{}

func (TestAdapter) Available(_ context.Context, member repository.Member, _ string) bool {
	return member.Endpoint != "test://unavailable"
}

type Resolver struct {
	Store   repository.Store
	Adapter Adapter
	Metrics *Metrics
}

func (r Resolver) Resolve(ctx context.Context, groupName, repositoryName, actor string) (repository.Member, error) {
	return r.resolve(ctx, groupName, repositoryName, actor, func(member repository.Member) bool {
		return r.Adapter.Available(ctx, member, repositoryName)
	})
}

func (r Resolver) ResolveOCIMembers(ctx context.Context, groupName, repositoryName, actor string) ([]repository.Member, error) {
	resolved := false
	defer func() {
		if !resolved {
			r.Metrics.failed.Add(1)
		}
	}()
	group, err := r.loadActiveGroup(ctx, groupName, repositoryName, actor)
	if err != nil {
		return nil, err
	}
	var hosted, proxy []repository.Member
	for _, member := range group.Members {
		if actor == "anonymous" && !member.Anonymous {
			continue
		}
		if !r.Adapter.Available(ctx, member, repositoryName) {
			continue
		}
		switch member.Type {
		case repository.MemberHosted:
			hosted = append(hosted, member)
		case repository.MemberProxy:
			proxy = append(proxy, member)
		}
	}
	if len(hosted)+len(proxy) == 0 {
		if err := r.audit(ctx, groupName, repositoryName, "", repository.AuditNotFound, actor); err != nil {
			return nil, err
		}
		return nil, repository.ErrNotFound
	}
	resolved = true
	return append(hosted, proxy...), nil
}

func (r Resolver) RecordOCIResolution(ctx context.Context, groupName, repositoryName, memberName, actor string) error {
	if err := r.audit(ctx, groupName, repositoryName, memberName, repository.AuditResolved, actor); err != nil {
		r.Metrics.failed.Add(1)
		return err
	}
	r.Metrics.resolved.Add(1)
	return nil
}

func (r Resolver) RecordOCIFailure(ctx context.Context, groupName, repositoryName, memberName, actor string, outcome repository.AuditOutcome) error {
	return r.audit(ctx, groupName, repositoryName, memberName, outcome, actor)
}

func (r Resolver) RecordOCIRequestFailure() {
	r.Metrics.failed.Add(1)
}

func (r Resolver) RecordOCIAnonymousDenied(ctx context.Context, groupName, repositoryName, resource, method string, status int) error {
	if err := r.Store.RecordAudit(ctx, repository.AuditRecord{
		GroupName: groupName, Repository: repositoryName, Actor: "anonymous", Outcome: repository.AuditAccessDenied, OccurredAt: time.Now().UTC(),
		Format: "oci", Resource: resource, Representation: resource, Operation: strings.ToLower(method), Status: status, CacheDisposition: "bypass",
	}); err != nil {
		return fmt.Errorf("record OCI anonymous denial: %w", err)
	}
	r.Metrics.recordAudit(repositoryName, repository.AuditAccessDenied)
	r.Metrics.failed.Add(1)
	return nil
}

func (r Resolver) resolve(ctx context.Context, groupName, repositoryName, actor string, eligible func(repository.Member) bool) (repository.Member, error) {
	resolved := false
	defer func() {
		if !resolved {
			r.Metrics.failed.Add(1)
		}
	}()
	group, err := r.loadActiveGroup(ctx, groupName, repositoryName, actor)
	if err != nil {
		return repository.Member{}, err
	}
	for _, member := range group.Members {
		if eligible(member) {
			if err := r.audit(ctx, groupName, repositoryName, member.Name, repository.AuditResolved, actor); err != nil {
				return repository.Member{}, err
			}
			r.Metrics.resolved.Add(1)
			resolved = true
			return member, nil
		}
	}
	if err := r.audit(ctx, groupName, repositoryName, "", repository.AuditNotFound, actor); err != nil {
		return repository.Member{}, err
	}
	return repository.Member{}, repository.ErrNotFound
}

func (r Resolver) loadActiveGroup(ctx context.Context, groupName, repositoryName, actor string) (repository.Group, error) {
	group, err := r.Store.GetGroup(ctx, groupName)
	if err != nil {
		outcome := repository.AuditStorageError
		if errors.Is(err, repository.ErrNotFound) {
			outcome = repository.AuditNotFound
		}
		if auditErr := r.audit(ctx, groupName, repositoryName, "", outcome, actor); auditErr != nil {
			return repository.Group{}, auditErr
		}
		return repository.Group{}, err
	}
	if !group.Enabled {
		if auditErr := r.audit(ctx, groupName, repositoryName, "", repository.AuditGroupDisabled, actor); auditErr != nil {
			return repository.Group{}, auditErr
		}
		return repository.Group{}, repository.ErrDisabled
	}
	return group, nil
}

func (r Resolver) audit(ctx context.Context, groupName, repositoryName, memberName string, outcome repository.AuditOutcome, actor string) error {
	if err := r.Store.RecordAudit(ctx, repository.AuditRecord{GroupName: groupName, Repository: repositoryName, MemberName: memberName, Outcome: outcome, Actor: actor, OccurredAt: time.Now().UTC()}); err != nil {
		return fmt.Errorf("record resolver audit: %w", err)
	}
	r.Metrics.recordAudit(repositoryName, outcome)
	return nil
}

type Metrics struct {
	resolved                 atomic.Uint64
	failed                   atomic.Uint64
	ociCacheHit              atomic.Uint64
	ociCacheMiss             atomic.Uint64
	ociCircuitOpen           atomic.Uint64
	ociNegativeHit           atomic.Uint64
	ociProxyDenied           atomic.Uint64
	mavenCacheHit            atomic.Uint64
	mavenCacheMiss           atomic.Uint64
	mavenCircuitOpen         atomic.Uint64
	mavenRetry               atomic.Uint64
	mavenNegativeHit         atomic.Uint64
	mavenProxyDenied         atomic.Uint64
	mavenCacheInvalidated    atomic.Uint64
	cacheQuotaDenied         atomic.Uint64
	anonymousReads           atomic.Uint64
	rawGetRequests           atomic.Uint64
	rawHeadRequests          atomic.Uint64
	rawOtherRequests         atomic.Uint64
	rawAuthorizationDenied   atomic.Uint64
	rawCacheHit              atomic.Uint64
	rawCacheMiss             atomic.Uint64
	rawNegativeHit           atomic.Uint64
	rawProxyDenied           atomic.Uint64
	rawChecksumFailure       atomic.Uint64
	rawUpstreamFailure       atomic.Uint64
	rawResponseBytes         atomic.Uint64
	conanGetRequests         atomic.Uint64
	conanHeadRequests        atomic.Uint64
	conanOtherRequests       atomic.Uint64
	conanAuthorizationDenied atomic.Uint64
	conanCacheHit            atomic.Uint64
	conanCacheMiss           atomic.Uint64
	conanNegativeHit         atomic.Uint64
	conanProxyDenied         atomic.Uint64
	conanChecksumFailure     atomic.Uint64
	conanUpstreamFailure     atomic.Uint64
	conanResponseBytes       atomic.Uint64
	conanCacheQuotaDenied    atomic.Uint64

	mu           sync.RWMutex
	repositories map[string]RepositoryMetrics
}

const maxRepositoryMetrics = 1000

// RepositoryMetrics is the bounded, in-process operational view for one
// repository. The gateway's audit log remains the durable request history.
type RepositoryMetrics struct {
	Requests       uint64 `json:"requests"`
	UpstreamErrors uint64 `json:"upstream_errors"`
	CacheHits      uint64 `json:"cache_hits"`
	CacheMisses    uint64 `json:"cache_misses"`
}

func (m *Metrics) recordAudit(repositoryName string, outcome repository.AuditOutcome) {
	m.updateRepository(repositoryName, func(metric *RepositoryMetrics) {
		if outcome == repository.AuditUpstreamError {
			metric.UpstreamErrors++
		}
	})
}

func (m *Metrics) recordRequest(repositoryName string) {
	m.updateRepository(repositoryName, func(metric *RepositoryMetrics) { metric.Requests++ })
}

func (m *Metrics) recordAnonymousRead() { m.anonymousReads.Add(1) }

func (m *Metrics) recordRawRequest(method string) {
	switch strings.ToLower(method) {
	case "get":
		m.rawGetRequests.Add(1)
	case "head":
		m.rawHeadRequests.Add(1)
	default:
		m.rawOtherRequests.Add(1)
	}
}

func (m *Metrics) recordRawAudit(outcome repository.AuditOutcome, bytes int64, checksumFailure bool) {
	switch outcome {
	case repository.AuditAccessDenied:
		m.rawAuthorizationDenied.Add(1)
	case repository.AuditProxyDenied:
		m.rawProxyDenied.Add(1)
	case repository.AuditUpstreamError:
		if checksumFailure {
			m.rawChecksumFailure.Add(1)
		} else {
			m.rawUpstreamFailure.Add(1)
		}
	}
	if bytes > 0 {
		m.rawResponseBytes.Add(uint64(bytes))
	}
}

func (m *Metrics) recordRawCacheHit()         { m.rawCacheHit.Add(1) }
func (m *Metrics) recordRawCacheMiss()        { m.rawCacheMiss.Add(1) }
func (m *Metrics) recordRawNegativeCacheHit() { m.rawNegativeHit.Add(1) }

func (m *Metrics) recordConanRequest(method string) {
	switch strings.ToLower(method) {
	case "get":
		m.conanGetRequests.Add(1)
	case "head":
		m.conanHeadRequests.Add(1)
	default:
		m.conanOtherRequests.Add(1)
	}
}
func (m *Metrics) recordConanAudit(outcome repository.AuditOutcome, bytes int64, checksumFailure bool) {
	switch outcome {
	case repository.AuditAccessDenied:
		m.conanAuthorizationDenied.Add(1)
	case repository.AuditProxyDenied:
		m.conanProxyDenied.Add(1)
	case repository.AuditUpstreamError:
		if checksumFailure {
			m.conanChecksumFailure.Add(1)
		} else {
			m.conanUpstreamFailure.Add(1)
		}
	}
	if bytes > 0 {
		m.conanResponseBytes.Add(uint64(bytes))
	}
}
func (m *Metrics) recordConanCacheHit()         { m.conanCacheHit.Add(1) }
func (m *Metrics) recordConanCacheMiss()        { m.conanCacheMiss.Add(1) }
func (m *Metrics) recordConanNegativeCacheHit() { m.conanNegativeHit.Add(1) }
func (m *Metrics) recordCache(repositoryName string, hit bool) {
	m.updateRepository(repositoryName, func(metric *RepositoryMetrics) {
		if hit {
			metric.CacheHits++
		} else {
			metric.CacheMisses++
		}
	})
}

func (m *Metrics) updateRepository(repositoryName string, update func(*RepositoryMetrics)) {
	if repositoryName == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.repositories == nil {
		m.repositories = make(map[string]RepositoryMetrics)
	}
	metric, exists := m.repositories[repositoryName]
	if !exists && len(m.repositories) >= maxRepositoryMetrics {
		return
	}
	update(&metric)
	m.repositories[repositoryName] = metric
}

func (m *Metrics) repository(repositoryName string) RepositoryMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.repositories[repositoryName]
}

func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte("# TYPE artifact_gateway_cache_quota_rejections_total counter\nartifact_gateway_cache_quota_rejections_total " + utoa(m.cacheQuotaDenied.Load()) + "\n# TYPE artifact_gateway_anonymous_reads_total counter\nartifact_gateway_anonymous_reads_total " + utoa(m.anonymousReads.Load()) + "\n"))
	_, _ = w.Write([]byte("# TYPE artifact_gateway_resolver_requests_total counter\nartifact_gateway_resolver_requests_total{outcome=\"resolved\"} " + utoa(m.resolved.Load()) + "\nartifact_gateway_resolver_requests_total{outcome=\"failed\"} " + utoa(m.failed.Load()) + "\n# TYPE artifact_gateway_oci_cache_requests_total counter\nartifact_gateway_oci_cache_requests_total{outcome=\"hit\"} " + utoa(m.ociCacheHit.Load()) + "\nartifact_gateway_oci_cache_requests_total{outcome=\"miss\"} " + utoa(m.ociCacheMiss.Load()) + "\n# TYPE artifact_gateway_oci_upstream_circuit_open_total counter\nartifact_gateway_oci_upstream_circuit_open_total " + utoa(m.ociCircuitOpen.Load()) + "\n# TYPE artifact_gateway_oci_negative_cache_hits_total counter\nartifact_gateway_oci_negative_cache_hits_total " + utoa(m.ociNegativeHit.Load()) + "\n# TYPE artifact_gateway_oci_proxy_denied_total counter\nartifact_gateway_oci_proxy_denied_total " + utoa(m.ociProxyDenied.Load()) + "\n# TYPE artifact_gateway_maven_cache_requests_total counter\nartifact_gateway_maven_cache_requests_total{outcome=\"hit\"} " + utoa(m.mavenCacheHit.Load()) + "\nartifact_gateway_maven_cache_requests_total{outcome=\"miss\"} " + utoa(m.mavenCacheMiss.Load()) + "\n# TYPE artifact_gateway_maven_upstream_circuit_open_total counter\nartifact_gateway_maven_upstream_circuit_open_total " + utoa(m.mavenCircuitOpen.Load()) + "\n# TYPE artifact_gateway_maven_upstream_retries_total counter\nartifact_gateway_maven_upstream_retries_total " + utoa(m.mavenRetry.Load()) + "\n# TYPE artifact_gateway_maven_negative_cache_hits_total counter\nartifact_gateway_maven_negative_cache_hits_total " + utoa(m.mavenNegativeHit.Load()) + "\n# TYPE artifact_gateway_maven_proxy_denied_total counter\nartifact_gateway_maven_proxy_denied_total " + utoa(m.mavenProxyDenied.Load()) + "\n# TYPE artifact_gateway_maven_cache_invalidations_total counter\nartifact_gateway_maven_cache_invalidations_total " + utoa(m.mavenCacheInvalidated.Load()) + "\n"))
	_, _ = w.Write([]byte("# TYPE artifact_gateway_raw_requests_total counter\nartifact_gateway_raw_requests_total{method=\"get\"} " + utoa(m.rawGetRequests.Load()) + "\nartifact_gateway_raw_requests_total{method=\"head\"} " + utoa(m.rawHeadRequests.Load()) + "\nartifact_gateway_raw_requests_total{method=\"other\"} " + utoa(m.rawOtherRequests.Load()) + "\n# TYPE artifact_gateway_raw_authorization_denials_total counter\nartifact_gateway_raw_authorization_denials_total " + utoa(m.rawAuthorizationDenied.Load()) + "\n# TYPE artifact_gateway_raw_cache_requests_total counter\nartifact_gateway_raw_cache_requests_total{outcome=\"hit\"} " + utoa(m.rawCacheHit.Load()) + "\nartifact_gateway_raw_cache_requests_total{outcome=\"miss\"} " + utoa(m.rawCacheMiss.Load()) + "\n# TYPE artifact_gateway_raw_negative_cache_hits_total counter\nartifact_gateway_raw_negative_cache_hits_total " + utoa(m.rawNegativeHit.Load()) + "\n# TYPE artifact_gateway_raw_proxy_denied_total counter\nartifact_gateway_raw_proxy_denied_total " + utoa(m.rawProxyDenied.Load()) + "\n# TYPE artifact_gateway_raw_checksum_failures_total counter\nartifact_gateway_raw_checksum_failures_total " + utoa(m.rawChecksumFailure.Load()) + "\n# TYPE artifact_gateway_raw_upstream_failures_total counter\nartifact_gateway_raw_upstream_failures_total " + utoa(m.rawUpstreamFailure.Load()) + "\n# TYPE artifact_gateway_raw_response_bytes_total counter\nartifact_gateway_raw_response_bytes_total " + utoa(m.rawResponseBytes.Load()) + "\n"))
	_, _ = w.Write([]byte("# TYPE artifact_gateway_conan_requests_total counter\nartifact_gateway_conan_requests_total{method=\"get\"} " + utoa(m.conanGetRequests.Load()) + "\nartifact_gateway_conan_requests_total{method=\"head\"} " + utoa(m.conanHeadRequests.Load()) + "\nartifact_gateway_conan_requests_total{method=\"other\"} " + utoa(m.conanOtherRequests.Load()) + "\n# TYPE artifact_gateway_conan_authorization_denials_total counter\nartifact_gateway_conan_authorization_denials_total " + utoa(m.conanAuthorizationDenied.Load()) + "\n# TYPE artifact_gateway_conan_cache_requests_total counter\nartifact_gateway_conan_cache_requests_total{outcome=\"hit\"} " + utoa(m.conanCacheHit.Load()) + "\nartifact_gateway_conan_cache_requests_total{outcome=\"miss\"} " + utoa(m.conanCacheMiss.Load()) + "\n# TYPE artifact_gateway_conan_negative_cache_hits_total counter\nartifact_gateway_conan_negative_cache_hits_total " + utoa(m.conanNegativeHit.Load()) + "\n# TYPE artifact_gateway_conan_proxy_denied_total counter\nartifact_gateway_conan_proxy_denied_total " + utoa(m.conanProxyDenied.Load()) + "\n# TYPE artifact_gateway_conan_checksum_failures_total counter\nartifact_gateway_conan_checksum_failures_total " + utoa(m.conanChecksumFailure.Load()) + "\n# TYPE artifact_gateway_conan_upstream_failures_total counter\nartifact_gateway_conan_upstream_failures_total " + utoa(m.conanUpstreamFailure.Load()) + "\n# TYPE artifact_gateway_conan_response_bytes_total counter\nartifact_gateway_conan_response_bytes_total " + utoa(m.conanResponseBytes.Load()) + "\n# TYPE artifact_gateway_conan_cache_quota_rejections_total counter\nartifact_gateway_conan_cache_quota_rejections_total " + utoa(m.conanCacheQuotaDenied.Load()) + "\n"))
}

func utoa(value uint64) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}

type Principal struct {
	Actor              string
	Admin              bool
	RepositoryPatterns []string
}

type Authenticator struct {
	AdminToken    string
	ResolverToken string
	AdminActor    string
	ResolverActor string
	// RepositoryReaders maps an actor to exact repository names or prefix
	// patterns ending in /*. A nil map keeps the local-development default.
	RepositoryReaders map[string][]string
	// RepositoryWriters is intentionally separate from readers: Maven deploy
	// must never turn a download grant into publication authority.
	RepositoryWriters map[string][]string
	OIDC              *OIDCValidator
}

func (a Authenticator) Authenticate(header string) (Principal, bool) {
	const bearer = "Bearer "
	if !strings.HasPrefix(header, bearer) {
		return Principal{}, false
	}
	token := strings.TrimPrefix(header, bearer)
	if tokenMatches(token, a.AdminToken) {
		return Principal{Actor: a.AdminActor, Admin: true}, true
	}
	if tokenMatches(token, a.ResolverToken) {
		return a.principal(a.ResolverActor), true
	}
	if actor, ok := a.tokenActor(token); ok {
		return a.principal(actor), true
	}
	if a.OIDC != nil {
		if identity, ok := a.OIDC.Validate(context.Background(), token); ok {
			principal := a.principal(identity.Subject)
			principal.Admin = identity.Admin
			return principal, true
		}
	}
	return Principal{}, false
}

func (a Authenticator) principal(actor string) Principal {
	return Principal{Actor: actor, RepositoryPatterns: a.RepositoryReaders[actor]}
}

func (p Principal) CanReadRepository(repositoryName string, policyConfigured bool) bool {
	if p.Admin || !policyConfigured {
		return true
	}
	for _, pattern := range p.RepositoryPatterns {
		if pattern == repositoryName || strings.HasSuffix(pattern, "/*") && strings.HasPrefix(repositoryName, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func (a Authenticator) CanReadRepository(principal Principal, repositoryName string) bool {
	return principal.CanReadRepository(repositoryName, a.RepositoryReaders != nil)
}

// CanReadMavenRepository treats a Maven Group as its repository boundary.
// `group/*` is accepted for compatibility with path-shaped grant policies,
// without broadening OCI's wildcard semantics.
func (a Authenticator) CanReadMavenRepository(principal Principal, groupName string) bool {
	if a.CanReadRepository(principal, groupName) {
		return true
	}
	for _, pattern := range principal.RepositoryPatterns {
		if strings.TrimSuffix(pattern, "/*") == groupName && strings.HasSuffix(pattern, "/*") {
			return true
		}
	}
	return false
}

func (a Authenticator) CanWriteMavenRepository(principal Principal, repositoryName string) bool {
	if principal.Admin {
		return true
	}
	for _, pattern := range a.RepositoryWriters[principal.Actor] {
		if pattern == repositoryName || strings.HasSuffix(pattern, "/*") && strings.HasPrefix(repositoryName, strings.TrimSuffix(pattern, "*")) {
			return true
		}
	}
	return false
}

func (a Authenticator) IssueToken(actor string) string {
	expiresAt := time.Now().UTC().Add(5 * time.Minute).Unix()
	payload := "v1." + base64.RawURLEncoding.EncodeToString([]byte(actor)) + "." + strconv.FormatInt(expiresAt, 10)
	mac := hmac.New(sha256.New, []byte(a.ResolverToken))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a Authenticator) tokenActor(token string) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != "v1" || a.ResolverToken == "" {
		return "", false
	}
	actor, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(actor) == 0 {
		return "", false
	}
	expiresAt, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().UTC().Unix() >= expiresAt {
		return "", false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, []byte(a.ResolverToken))
	_, _ = mac.Write([]byte(strings.Join(parts[:3], ".")))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", false
	}
	return string(actor), true
}

func tokenMatches(value, expected string) bool {
	return expected != "" && len(value) == len(expected) && subtle.ConstantTimeCompare([]byte(value), []byte(expected)) == 1
}

type GatewayStore interface {
	repository.Store
	repository.MavenStore
	repository.RawStore
	repository.ConanStore
	repository.HostedRepositoryStore
	repository.HostedGroupStore
	repository.NativeMavenStore
	repository.NativeOCIStore
	repository.NativeRawStore
}

func NewGatewayHandler(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, ociClients ...OCIClient) http.Handler {
	return NewGatewayHandlerWithOCICache(dependencies, store, adapter, authenticator, NewDefaultOCICache(NewMemoryOCIObjectStore(), nil), ociClients...)
}

func NewGatewayHandlerWithOCICache(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, ociClients ...OCIClient) http.Handler {
	return NewGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, nil, ociClients...)
}

func NewGatewayHandlerWithCaches(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, nil, NewConanCache(nil), nil, ociClients...)
}

func NewGatewayHandlerWithCacheMaintenance(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, nil, NewConanCache(nil), maintenance, ociClients...)
}

func NewGatewayHandlerWithRawCache(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, rawCache, NewConanCache(nil), maintenance, ociClients...)
}

func NewGatewayHandlerWithFormatCaches(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, conanCache *ConanCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	return newGatewayHandlerWithCaches(dependencies, store, adapter, authenticator, cache, mavenCache, rawCache, conanCache, maintenance, ociClients...)
}

func newGatewayHandlerWithCaches(dependencies Dependencies, store GatewayStore, adapter Adapter, authenticator Authenticator, cache *OCICache, mavenCache *MavenCache, rawCache *RawCache, conanCache *ConanCache, maintenance *CacheMaintenance, ociClients ...OCIClient) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /readyz", dependencies.ready)
	metrics := &Metrics{}
	resolver := Resolver{Store: store, Adapter: adapter, Metrics: metrics}
	api := apiHandler{store: store, resolver: resolver, authenticator: authenticator}
	ociClient := OCIClient(UpstreamClient{})
	if len(ociClients) > 0 {
		ociClient = ociClients[0]
	}
	mavenClient := MavenClient(UpstreamClient{})
	if client, ok := ociClient.(MavenClient); ok {
		mavenClient = client
	}
	rawClient := RawClient(UpstreamClient{})
	if client, ok := ociClient.(RawClient); ok {
		rawClient = client
	}
	conanClient := ConanClient(UpstreamClient{})
	if client, ok := ociClient.(ConanClient); ok {
		conanClient = client
	}
	oci := OCIHandler{Resolver: resolver, Client: ociClient, Authenticator: authenticator, Cache: cache}
	nativeOCI := newNativeOCIHandler(store, dependencies.NativeOCIObjectStore, authenticator)
	nativeRaw := newNativeRawHandler(store, dependencies.NativeOCIObjectStore, authenticator)
	mux.Handle("GET /metrics", http.HandlerFunc(metrics.Handler))
	mux.Handle("/api/v1/oci/groups", api)
	mux.Handle("/api/v1/oci/groups/", api)
	mux.Handle("/api/v1/maven/groups", mavenAPIHandler{store: store, authenticator: authenticator})
	mux.Handle("/api/v1/maven/groups/", mavenAPIHandler{store: store, authenticator: authenticator})
	rawAPI := rawAPIHandler{store: store, authenticator: authenticator, cache: rawCache}
	mux.Handle("/api/v1/raw/groups", rawAPI)
	mux.Handle("/api/v1/raw/groups/", rawAPI)
	mux.Handle("POST /api/v1/raw/cache/invalidate", rawCacheInvalidationHandler{store: store, authenticator: authenticator, cache: rawCache})
	conanAPI := conanAPIHandler{store: store, authenticator: authenticator}
	mux.Handle("/api/v1/conan/groups", conanAPI)
	mux.Handle("/api/v1/conan/groups/", conanAPI)
	nativeObjects := dependencies.NativeMavenObjectStore
	if nativeObjects == nil {
		nativeObjects = NewMemoryOCIObjectStore()
	}
	nativeMaven := newNativeMavenHandler(store, nativeObjects, authenticator)
	hostedRepositories := hostedRepositoryAPIHandler{store: store, authenticator: authenticator}
	adminopenapi.HandlerWithOptions(generatedRepositoryAPIAdapter{hostedRepositoryAPIHandler: hostedRepositories, sessions: nativeMaven, groups: store}, adminopenapi.StdHTTPServerOptions{
		BaseURL:    "/api/v2",
		BaseRouter: openAPIServeMux{mux: mux, authorize: hostedRepositories.authorize},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeHostedProblem(w, http.StatusBadRequest, "invalid_request", err.Error())
		},
	})
	mux.Handle("/api/v2/repositories/", nativeMaven)
	mux.Handle("/api/v2/publish-sessions/", nativeMaven)
	mux.Handle("POST /api/v1/conan/cache/invalidate", conanCacheInvalidationHandler{store: store, authenticator: authenticator, cache: conanCache})
	mux.Handle("GET /api/v1/audits", auditAPIHandler{store: store, authenticator: authenticator})
	if maintenance != nil {
		mux.Handle("GET /api/v1/operations/cache", cacheOperationsHandler{maintenance: maintenance, authenticator: authenticator})
		mux.Handle("POST /api/v1/operations/cache/collect", cacheCollectionHandler{maintenance: maintenance, authenticator: authenticator})
		mux.Handle("GET /api/v1/operations/repositories", repositoryOperationsHandler{maintenance: maintenance, metrics: metrics, authenticator: authenticator})
	}
	mux.Handle("/v2/", hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatOCI, next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nativeOCI.ServeHTTP(w, r) {
			return
		}
		oci.ServeHTTP(w, r)
	})})
	mux.Handle("/repository/maven/", nativeMaven)
	mux.Handle("/maven/", hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatMaven, next: MavenHandler{Store: store, Authenticator: authenticator, Client: mavenClient, Metrics: metrics, Cache: mavenCache}})
	mux.Handle("/raw/", hostedRepositoryGuard{store: store, authenticator: authenticator, format: repository.FormatRaw, next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if nativeRaw.ServeHTTP(w, r) {
			return
		}
		RawHandler{Store: store, Authenticator: authenticator, Client: rawClient, Metrics: metrics, Cache: rawCache}.ServeHTTP(w, r)
	})})
	mux.Handle("/conan/v2/", ConanHandler{Store: store, Authenticator: authenticator, Client: conanClient, Metrics: metrics, Cache: conanCache})
	mux.Handle("/conan/", ConanHandler{Store: store, Authenticator: authenticator, Client: conanClient, Metrics: metrics, Cache: conanCache})
	mux.HandleFunc("GET /auth/token", oci.Token)
	return tracedHTTPHandler(mux)
}

type cacheOperationsHandler struct {
	maintenance   *CacheMaintenance
	authenticator Authenticator
}

type cacheCollectionHandler struct {
	maintenance   *CacheMaintenance
	authenticator Authenticator
}

func (h cacheCollectionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !principal.Admin {
		http.Error(w, "administrator access required", http.StatusForbidden)
		return
	}
	if err := h.maintenance.Run(r.Context()); err != nil {
		http.Error(w, "cache collection failed", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h cacheOperationsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	status, err := h.maintenance.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to inspect cache")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

type RepositoryOperationsStatus struct {
	Repository   string                 `json:"repository"`
	Metrics      RepositoryMetrics      `json:"metrics"`
	HitRate      float64                `json:"hit_rate"`
	GatewayCache CacheMaintenanceStatus `json:"gateway_cache"`
}

type repositoryOperationsHandler struct {
	maintenance   *CacheMaintenance
	metrics       *Metrics
	authenticator Authenticator
}

func (h repositoryOperationsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	repositoryName := strings.TrimSpace(r.URL.Query().Get("repository"))
	if repositoryName == "" {
		writeError(w, http.StatusBadRequest, "invalid_repository", "repository is required")
		return
	}
	cache, err := h.maintenance.Status(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to inspect cache")
		return
	}
	metrics := h.metrics.repository(repositoryName)
	denominator := metrics.CacheHits + metrics.CacheMisses
	status := RepositoryOperationsStatus{Repository: repositoryName, Metrics: metrics, GatewayCache: cache}
	if denominator > 0 {
		status.HitRate = float64(metrics.CacheHits) / float64(denominator)
	}
	writeJSON(w, http.StatusOK, status)
}

type auditAPIHandler struct {
	store         repository.AuditStore
	authenticator Authenticator
}

func (a auditAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}
	audits, err := a.store.ListAudits(r.Context(), repository.AuditQuery{
		GroupName: r.URL.Query().Get("group"), Repository: r.URL.Query().Get("repository"), Limit: limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to query audits")
		return
	}
	writeJSON(w, http.StatusOK, audits)
}

type mavenAPIHandler struct {
	store         repository.MavenStore
	authenticator Authenticator
}

type conanAPIHandler struct {
	store         repository.ConanStore
	authenticator Authenticator
}
type conanCacheInvalidationHandler struct {
	store         repository.ConanStore
	authenticator Authenticator
	cache         *ConanCache
}

func (h conanCacheInvalidationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !p.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	if h.cache == nil {
		writeError(w, http.StatusServiceUnavailable, "cache_unavailable", "Conan cache is not configured")
		return
	}
	defer r.Body.Close()
	var request struct{ Group, Path, Member, Endpoint string }
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if request.Group == "" || request.Path == "" || request.Member == "" || request.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "group, path, member and endpoint are required")
		return
	}
	if _, _, _, _, valid := parseConanPath(http.MethodGet, "/conan/v2/"+request.Group+"/conans/"+request.Path); !valid {
		writeError(w, http.StatusBadRequest, "invalid_path", "path must be a supported Conan read endpoint")
		return
	}
	group, err := h.store.GetConanGroup(r.Context(), request.Group)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
		return
	}
	for _, member := range group.Members {
		if member.Name == request.Member && member.Endpoint == request.Endpoint {
			h.cache.Invalidate(r.Context(), request.Group, request.Path, member)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "member not found")
}

type rawAPIHandler struct {
	store         repository.RawStore
	authenticator Authenticator
	cache         *RawCache
}

func (a rawAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !p.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/raw/groups")
	if path == "" || path == "/" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		defer r.Body.Close()
		var g repository.Group
		if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if err := validateRawGroup(g); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
			return
		}
		created, err := a.store.CreateRawGroup(r.Context(), g)
		if errors.Is(err, repository.ErrNameExists) {
			writeError(w, http.StatusConflict, "group_exists", "group name already exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		g, err := a.store.GetRawGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
			return
		}
		writeJSON(w, http.StatusOK, g)
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		err := a.store.DisableRawGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to disable group")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

type rawCacheInvalidationHandler struct {
	store         repository.RawStore
	authenticator Authenticator
	cache         *RawCache
}

type rawInvalidationRequest struct {
	Group    string `json:"group"`
	Path     string `json:"path"`
	Member   string `json:"member,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

func (h rawCacheInvalidationHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok || !p.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	if h.cache == nil {
		writeError(w, http.StatusServiceUnavailable, "cache_unavailable", "Raw cache is not configured")
		return
	}
	defer func() { _ = r.Body.Close() }()
	var request rawInvalidationRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if !validRawGroupName(request.Group) {
		writeError(w, http.StatusBadRequest, "invalid_group", "group must be a non-reserved DNS label")
		return
	}
	resource, valid := v2contract.NewCanonicalResource(request.Path)
	if !valid || resource.String() != request.Path {
		writeError(w, http.StatusBadRequest, "invalid_path", "path must be canonical")
		return
	}
	if request.Member == "" || request.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "invalid_member", "member and endpoint are required")
		return
	}
	group, err := h.store.GetRawGroup(r.Context(), request.Group)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
		return
	}
	matched := false
	for _, member := range group.Members {
		if member.Name != request.Member || member.Endpoint != request.Endpoint {
			continue
		}
		h.cache.Invalidate(r.Context(), h.cache.key(request.Group, resource.String(), member.Name, member.Endpoint))
		matched = true
	}
	if !matched {
		writeError(w, http.StatusNotFound, "not_found", "member not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a conanAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !p.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/conan/groups")
	if path == "" || path == "/" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		defer func() { _ = r.Body.Close() }()
		var group repository.Group
		if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if err := validateConanGroup(group); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
			return
		}
		created, err := a.store.CreateConanGroup(r.Context(), group)
		if errors.Is(err, repository.ErrNameExists) {
			writeError(w, http.StatusConflict, "group_exists", "group name already exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		group, err := a.store.GetConanGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
			return
		}
		writeJSON(w, http.StatusOK, group)
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		err := a.store.DisableConanGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to disable group")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func (a mavenAPIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	if !principal.Admin {
		writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/maven/groups")
	if path == "" || path == "/" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		defer func() { _ = r.Body.Close() }()
		var group repository.Group
		if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
			return
		}
		if err := validateGroup(group); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
			return
		}
		created, err := a.store.CreateMavenGroup(r.Context(), group)
		if errors.Is(err, repository.ErrNameExists) {
			writeError(w, http.StatusConflict, "group_exists", "group name already exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		group, err := a.store.GetMavenGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to read group")
			return
		}
		writeJSON(w, http.StatusOK, group)
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		err := a.store.DisableMavenGroup(r.Context(), parts[0])
		if errors.Is(err, repository.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "group not found")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to disable group")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

type apiHandler struct {
	store         repository.Store
	resolver      Resolver
	authenticator Authenticator
}

func (a apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	principal, ok := a.authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/oci/groups")
	if path == "" || path == "/" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if !a.requireAdmin(w, principal) {
			return
		}
		a.create(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		if !a.requireAdmin(w, principal) {
			return
		}
		a.get(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "disable" && r.Method == http.MethodPost {
		if !a.requireAdmin(w, principal) {
			return
		}
		a.disable(w, r, parts[0])
		return
	}
	if len(parts) == 2 && parts[1] == "resolve" && r.Method == http.MethodGet {
		a.resolve(w, r, parts[0], principal)
		return
	}
	writeError(w, http.StatusNotFound, "not_found", "resource not found")
}

func (a apiHandler) requireAdmin(w http.ResponseWriter, principal Principal) bool {
	if principal.Admin {
		return true
	}
	writeError(w, http.StatusForbidden, "forbidden", "administrator permission required")
	return false
}

func (a apiHandler) create(w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	var group repository.Group
	if err := json.NewDecoder(r.Body).Decode(&group); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return
	}
	if err := validateGroup(group); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_group", err.Error())
		return
	}
	created, err := a.store.CreateGroup(r.Context(), group)
	if errors.Is(err, repository.ErrNameExists) {
		writeError(w, http.StatusConflict, "group_exists", "group name already exists")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "unable to create group")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a apiHandler) get(w http.ResponseWriter, r *http.Request, name string) {
	group, err := a.store.GetGroup(r.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, 500, "storage_error", "unable to read group")
		return
	}
	writeJSON(w, 200, group)
}
func (a apiHandler) disable(w http.ResponseWriter, r *http.Request, name string) {
	err := a.store.DisableGroup(r.Context(), name)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "group not found")
		return
	}
	if err != nil {
		writeError(w, 500, "storage_error", "unable to disable group")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a apiHandler) resolve(w http.ResponseWriter, r *http.Request, name string, principal Principal) {
	repositoryName := r.URL.Query().Get("repository")
	if repositoryName == "" {
		a.resolver.Metrics.failed.Add(1)
		writeError(w, 400, "invalid_repository", "repository query parameter is required")
		return
	}
	if !a.authenticator.CanReadRepository(principal, repositoryName) {
		if err := a.store.RecordAudit(r.Context(), repository.AuditRecord{GroupName: name, Repository: repositoryName, Outcome: repository.AuditAccessDenied, Actor: principal.Actor, OccurredAt: time.Now().UTC()}); err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "unable to record repository audit")
			return
		}
		a.resolver.Metrics.failed.Add(1)
		writeError(w, http.StatusForbidden, "forbidden", "repository read permission required")
		return
	}
	member, err := a.resolver.Resolve(r.Context(), name, repositoryName, principal.Actor)
	if errors.Is(err, repository.ErrDisabled) {
		writeError(w, 409, "group_disabled", "group is disabled")
		return
	}
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, 404, "not_found", "no member can serve repository")
		return
	}
	if err != nil {
		writeError(w, 500, "resolver_error", "unable to resolve repository")
		return
	}
	writeJSON(w, 200, member)
}

func validateGroup(group repository.Group) error {
	if group.Name == "" || strings.Contains(group.Name, "/") {
		return errors.New("name must be a non-empty OCI namespace")
	}
	if len(group.Members) == 0 {
		return errors.New("at least one member is required")
	}
	positions := make(map[int]bool, len(group.Members))
	for _, member := range group.Members {
		if member.Name == "" || member.Endpoint == "" {
			return errors.New("member name and endpoint are required")
		}
		if member.Type != repository.MemberHosted && member.Type != repository.MemberProxy {
			return errors.New("member type must be hosted or proxy")
		}
		if member.Position < 0 || positions[member.Position] {
			return errors.New("member positions must be unique non-negative integers")
		}
		positions[member.Position] = true
	}
	for position := range group.Members {
		if !positions[position] {
			return errors.New("member positions must start at zero and be contiguous")
		}
	}
	return nil
}

func validateRawGroup(group repository.Group) error {
	if err := validateGroup(group); err != nil {
		return err
	}
	if !validRawGroupName(group.Name) {
		return errors.New("name must be a non-reserved DNS label")
	}
	if group.CacheQuotaBytes <= 0 {
		return errors.New("cacheQuotaBytes must be a positive integer")
	}
	for _, member := range group.Members {
		if member.Type == repository.MemberProxy && len(member.AllowedHosts) == 0 {
			return errors.New("proxy members require a non-empty allowlist")
		}
	}
	return nil
}

func validateConanGroup(group repository.Group) error {
	if err := validateGroup(group); err != nil {
		return err
	}
	if !validRawGroupName(group.Name) {
		return errors.New("name must be a non-reserved DNS label")
	}
	if group.CacheQuotaBytes < 0 {
		return errors.New("cacheQuotaBytes must be a positive integer")
	}
	for _, member := range group.Members {
		if member.Type == repository.MemberProxy && len(member.AllowedHosts) == 0 {
			return errors.New("proxy members require a non-empty allowlist")
		}
	}
	return nil
}

func validRawGroupName(name string) bool {
	if len(name) == 0 || len(name) > 63 || rawReservedGroupNames[name] {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

var rawReservedGroupNames = map[string]bool{
	"api": true, "metrics": true, "livez": true, "readyz": true, "operations": true,
	"v2": true, "maven": true, "raw": true, "conan": true,
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
