package app

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type Metrics struct {
	resolved                       atomic.Uint64
	failed                         atomic.Uint64
	ociCacheHit                    atomic.Uint64
	ociCacheMiss                   atomic.Uint64
	ociCircuitOpen                 atomic.Uint64
	ociNegativeHit                 atomic.Uint64
	ociProxyDenied                 atomic.Uint64
	mavenCacheHit                  atomic.Uint64
	mavenCacheMiss                 atomic.Uint64
	mavenCircuitOpen               atomic.Uint64
	mavenRetry                     atomic.Uint64
	mavenNegativeHit               atomic.Uint64
	mavenProxyDenied               atomic.Uint64
	mavenCacheInvalidated          atomic.Uint64
	cacheQuotaDenied               atomic.Uint64
	anonymousReads                 atomic.Uint64
	rawGetRequests                 atomic.Uint64
	rawHeadRequests                atomic.Uint64
	rawOtherRequests               atomic.Uint64
	rawAuthorizationDenied         atomic.Uint64
	rawCacheHit                    atomic.Uint64
	rawCacheMiss                   atomic.Uint64
	rawNegativeHit                 atomic.Uint64
	rawProxyDenied                 atomic.Uint64
	rawChecksumFailure             atomic.Uint64
	rawUpstreamFailure             atomic.Uint64
	rawResponseBytes               atomic.Uint64
	conanGetRequests               atomic.Uint64
	conanHeadRequests              atomic.Uint64
	conanOtherRequests             atomic.Uint64
	conanAuthorizationDenied       atomic.Uint64
	conanCacheHit                  atomic.Uint64
	conanCacheMiss                 atomic.Uint64
	conanNegativeHit               atomic.Uint64
	conanProxyDenied               atomic.Uint64
	conanChecksumFailure           atomic.Uint64
	conanUpstreamFailure           atomic.Uint64
	conanResponseBytes             atomic.Uint64
	conanCacheQuotaDenied          atomic.Uint64
	repositoryAuthorizationDenials [repositoryAuthorizationFormatCount][repositoryGrantDenialReasonCount]atomic.Uint64

	mu           sync.RWMutex
	repositories map[string]RepositoryMetrics
}

type repositoryAuthorizationFormat uint8

const (
	repositoryAuthorizationFormatManagement repositoryAuthorizationFormat = iota
	repositoryAuthorizationFormatMaven
	repositoryAuthorizationFormatOCI
	repositoryAuthorizationFormatRaw
	repositoryAuthorizationFormatConan
	repositoryAuthorizationFormatCount
)

type repositoryGrantDenialReason uint8

const (
	repositoryGrantDenialScopeNotGranted repositoryGrantDenialReason = iota
	repositoryGrantDenialGrantLookupFailed
	repositoryGrantDenialReasonCount
)

var repositoryAuthorizationFormats = [...]string{"management", "maven", "oci", "raw", "conan"}
var repositoryGrantDenialReasons = [...]string{"scope_not_granted", "grant_lookup_failed"}

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

// recordRepositoryAuthorizationDenied accepts only bounded grant decision
// values. Actor, repository, path, and endpoint must never become labels.
func (m *Metrics) recordRepositoryAuthorizationDenied(format, source, reason string) {
	if source != "repository_grants" {
		return
	}
	formatIndex := -1
	for index, value := range repositoryAuthorizationFormats {
		if format == value {
			formatIndex = index
			break
		}
	}
	if formatIndex < 0 {
		return
	}
	for index, value := range repositoryGrantDenialReasons {
		if reason == value {
			m.repositoryAuthorizationDenials[formatIndex][index].Add(1)
			return
		}
	}
}

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
	_, _ = w.Write([]byte("# TYPE artifact_gateway_repository_authorization_denials_total counter\n"))
	for formatIndex, format := range repositoryAuthorizationFormats {
		for reasonIndex, reason := range repositoryGrantDenialReasons {
			_, _ = w.Write([]byte("artifact_gateway_repository_authorization_denials_total{format=\"" + format + "\",authorization_source=\"repository_grants\",authorization_reason=\"" + reason + "\"} " + utoa(m.repositoryAuthorizationDenials[formatIndex][reasonIndex].Load()) + "\n"))
		}
	}
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
