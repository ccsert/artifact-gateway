package raw

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	cache "github.com/artifact-gateway/artifact-gateway/internal/cache"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type Principal struct {
	Actor string
	Admin bool
	Role  string
}

type Decision struct {
	Allowed bool
	Source  string
	Reason  string
}

type AuditEvent struct {
	Member                                   repository.Member
	Actor                                    string
	Outcome                                  repository.AuditOutcome
	Status                                   int
	CacheDisposition                         string
	Bytes                                    int64
	Method                                   string
	ChecksumFailure                          bool
	AuthorizationSource, AuthorizationReason string
}

// Runtime is the protocol handler's complete application seam. It owns
// identity policy, managed-grant decisions, durable auditing and metrics.
type Runtime interface {
	Authenticate(string) (Principal, bool)
	AnonymousAllowed(context.Context, string) bool
	CanRead(Principal, string) bool
	ManagedDecision(context.Context, Principal, repository.Member, string) (Decision, bool)
	Prioritize([]repository.Member) []repository.Member
	Audit(context.Context, string, string, AuditEvent)
	RecordRequest(string, string)
	RecordRepositoryRequest(string)
	RecordAnonymousRead()
	RecordCacheHit()
	RecordCacheMiss()
	RecordNegativeCacheHit()
	RecordQuotaDenied()
	RecordSpoolRejected()
}

type Handler struct {
	Store   repository.RawStore
	Client  Client
	Cache   *Cache
	Runtime Runtime
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Runtime == nil {
		http.Error(w, "Raw runtime unavailable", http.StatusServiceUnavailable)
		return
	}
	h.Runtime.RecordRequest("", r.Method)
	if strings.HasSuffix(r.URL.EscapedPath(), "/") {
		h.audit(r.Context(), "", "", AuditEvent{Actor: "anonymous", Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: "bypass", Method: r.Method})
		http.NotFound(w, r)
		return
	}
	group, path, ok := ParsePath(r.URL.EscapedPath())
	if !ok {
		h.audit(r.Context(), "", "", AuditEvent{Actor: "anonymous", Outcome: repository.AuditStorageError, Status: http.StatusBadRequest, CacheDisposition: "bypass", Method: r.Method})
		http.Error(w, "invalid raw path", http.StatusBadRequest)
		return
	}
	p, authenticated := h.Runtime.Authenticate(r.Header.Get("Authorization"))
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.audit(r.Context(), group, path, AuditEvent{Actor: actor(p, authenticated), Outcome: repository.AuditAccessDenied, Status: http.StatusMethodNotAllowed, CacheDisposition: "bypass", Method: r.Method})
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !authenticated && h.Runtime.AnonymousAllowed(r.Context(), group) {
		p, authenticated = Principal{Actor: "anonymous"}, true
	}
	if !authenticated {
		h.audit(r.Context(), group, path, AuditEvent{Actor: "anonymous", Outcome: repository.AuditAccessDenied, Status: http.StatusUnauthorized, CacheDisposition: "bypass", Method: r.Method})
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Raw"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if p.Actor == "anonymous" {
		h.Runtime.RecordAnonymousRead()
	}
	h.Runtime.RecordRepositoryRequest(group)
	g, err := h.Store.GetRawGroup(r.Context(), group)
	if err != nil {
		h.audit(r.Context(), group, path, AuditEvent{Actor: p.Actor, Outcome: auditOutcome(err), Status: http.StatusNotFound, CacheDisposition: "bypass", Method: r.Method})
		http.NotFound(w, r)
		return
	}
	if !g.Enabled {
		h.audit(r.Context(), group, path, AuditEvent{Actor: p.Actor, Outcome: repository.AuditGroupDisabled, Status: http.StatusForbidden, CacheDisposition: "bypass", Method: r.Method})
		http.Error(w, "Raw group is disabled", http.StatusForbidden)
		return
	}
	if p.Actor != "anonymous" && !h.Runtime.CanRead(p, group) {
		h.audit(r.Context(), group, path, AuditEvent{Actor: p.Actor, Outcome: repository.AuditAccessDenied, Status: http.StatusForbidden, CacheDisposition: "bypass", Method: r.Method})
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	members := g.Members
	if p.Actor == "anonymous" {
		members = make([]repository.Member, 0, len(g.Members))
		for _, member := range g.Members {
			if member.Anonymous {
				members = append(members, member)
			}
		}
	}
	hadFailure, hadProxyDenied, hadAuthorizationDenied := false, false, false
	var lastNegative *repository.Member
	for _, member := range h.Runtime.Prioritize(members) {
		managed := false
		if p.Actor != "anonymous" {
			if decision, isManaged := h.Runtime.ManagedDecision(r.Context(), p, member, path); isManaged {
				managed = true
				if !decision.Allowed {
					h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditAccessDenied, Status: http.StatusForbidden, CacheDisposition: "bypass", Method: r.Method, AuthorizationSource: decision.Source, AuthorizationReason: decision.Reason})
					hadAuthorizationDenied = true
					continue
				}
			}
		}
		if p.Actor != "anonymous" && !managed && !h.Runtime.CanRead(p, member.Name) {
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditAccessDenied, Status: http.StatusForbidden, CacheDisposition: "bypass", Method: r.Method})
			http.Error(w, "repository read permission required", http.StatusForbidden)
			return
		}
		key := ""
		if h.Cache != nil {
			key = h.Cache.Key(group, path, member.Name, member.Endpoint)
			cached := h.serveCached(w, r, group, path, member, p, key, &lastNegative)
			if cached == cacheServed {
				return
			}
			if cached == cacheNegative {
				continue
			}
			h.Runtime.RecordCacheMiss()
		}
		if member.Type == repository.MemberProxy && !MemberProxyAllowed(member) {
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditProxyDenied, Status: http.StatusForbidden, CacheDisposition: "bypass", Method: r.Method})
			hadProxyDenied = true
			continue
		}
		if r.Method == http.MethodHead {
			response, fetchErr := h.Client.FetchRaw(r.Context(), http.MethodHead, member, path, nil)
			if fetchErr != nil {
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
				hadFailure = true
				continue
			}
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
				if h.Cache != nil {
					_ = h.Cache.StoreNegative(r.Context(), key, member)
				}
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: cacheDisposition(h.Cache), Method: r.Method})
				continue
			}
			if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
				hadFailure = true
				continue
			}
			copyHeadHeaders(w.Header(), response.Header)
			w.WriteHeader(http.StatusOK)
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditResolved, Status: http.StatusOK, CacheDisposition: cacheDisposition(h.Cache), Method: r.Method})
			return
		}
		workCtx := r.Context()
		release := func() error { return nil }
		if h.Cache != nil {
			var lockErr error
			workCtx, release, lockErr = h.Cache.AcquireRequestLock(r.Context(), key)
			if lockErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			content, loadErr := h.Cache.Load(workCtx, key)
			if loadErr == nil {
				h.Runtime.RecordCacheHit()
				if releaseErr := release(); releaseErr != nil {
					http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
					return
				}
				served := ServeContent(w, r, path, Content{Digest: content.Digest, ContentType: content.ContentType, Size: content.Size, Source: content})
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditResolved, Status: served.Status, CacheDisposition: "hit", Bytes: served.Bytes, Method: r.Method})
				return
			}
			if errors.Is(loadErr, ErrNegativeCache) {
				h.Runtime.RecordNegativeCacheHit()
				if releaseErr := release(); releaseErr != nil {
					http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
					return
				}
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: "hit", Method: r.Method})
				copy := member
				lastNegative = &copy
				continue
			}
		}
		releaseSpool := func() {}
		if h.Cache != nil {
			var spoolErr error
			releaseSpool, spoolErr = h.Cache.AcquireSpool()
			if spoolErr != nil {
				if releaseErr := release(); releaseErr != nil {
					http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Retry-After", "1")
				h.Runtime.RecordSpoolRejected()
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditStorageError, Status: http.StatusServiceUnavailable, CacheDisposition: "miss", Method: r.Method})
				http.Error(w, "Raw cache staging capacity is full", http.StatusServiceUnavailable)
				return
			}
		}
		releaseAll := func() error {
			releaseSpool()
			return release()
		}
		response, fetchErr := h.Client.FetchRaw(workCtx, http.MethodGet, member, path, nil)
		if fetchErr != nil {
			if releaseErr := releaseAll(); releaseErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
			hadFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
			_ = response.Body.Close()
			if h.Cache != nil {
				_ = h.Cache.StoreNegative(workCtx, key, member)
			}
			releaseErr := releaseAll()
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: cacheDisposition(h.Cache), Method: r.Method})
			if releaseErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			_ = response.Body.Close()
			if releaseErr := releaseAll(); releaseErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
			hadFailure = true
			continue
		}
		limit := int64(1 << 30)
		if h.Cache != nil && h.Cache.MaxObjectBytes() > 0 {
			limit = h.Cache.MaxObjectBytes()
		}
		content, readErr := StageContent(response.Body, limit)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			content.Cleanup()
			if releaseErr := releaseAll(); releaseErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
			hadFailure = true
			continue
		}
		content.ContentType, content.Member, content.Endpoint = response.Header.Get("Content-Type"), member.Name, member.Endpoint
		content.Repository, content.Path, content.CacheQuotaBytes = group, path, g.CacheQuotaBytes
		if content.ContentType == "" {
			content.ContentType = "application/octet-stream"
		}
		if strings.HasSuffix(path, ".sha256") || strings.HasSuffix(path, ".sha512") {
			if !validStagedChecksum(workCtx, path, content) {
				content.Cleanup()
				if releaseErr := releaseAll(); releaseErr != nil {
					http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
					return
				}
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method, ChecksumFailure: true})
				http.Error(w, "invalid checksum sidecar", http.StatusBadGateway)
				return
			}
		}
		if h.Cache != nil {
			if err := h.Cache.Store(workCtx, key, content); errors.Is(err, cache.ErrQuotaExceeded) {
				h.Runtime.RecordQuotaDenied()
			} else if err != nil {
				content.Cleanup()
				if releaseErr := releaseAll(); releaseErr != nil {
					http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
					return
				}
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusInternalServerError, CacheDisposition: "bypass", Method: r.Method})
				http.Error(w, "unable to cache Raw content", http.StatusInternalServerError)
				return
			}
		}
		if releaseErr := release(); releaseErr != nil {
			content.Cleanup()
			releaseSpool()
			http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
			return
		}
		served := ServeContent(w, r, path, Content{Digest: content.Digest, ContentType: content.ContentType, Size: content.Size, Source: content})
		content.Cleanup()
		releaseSpool()
		h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditResolved, Status: served.Status, CacheDisposition: cacheDisposition(h.Cache), Bytes: served.Bytes, Method: r.Method})
		return
	}
	if hadFailure {
		http.Error(w, "upstream repository unavailable", http.StatusBadGateway)
		return
	}
	if hadProxyDenied {
		http.Error(w, "upstream repository is not allowed", http.StatusForbidden)
		return
	}
	if hadAuthorizationDenied {
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	if lastNegative != nil {
		h.audit(r.Context(), group, path, AuditEvent{Member: *lastNegative, Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: "hit", Method: r.Method})
	} else {
		h.audit(r.Context(), group, path, AuditEvent{Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: cacheDisposition(h.Cache), Method: r.Method})
	}
	http.NotFound(w, r)
}

type cacheReadResult uint8

const (
	cacheMiss cacheReadResult = iota
	cacheServed
	cacheNegative
)

func (h Handler) serveCached(w http.ResponseWriter, r *http.Request, group, path string, member repository.Member, principal Principal, key string, lastNegative **repository.Member) cacheReadResult {
	content, err := h.Cache.Load(r.Context(), key)
	if err == nil {
		h.Runtime.RecordCacheHit()
		served := ServeContent(w, r, path, Content{Digest: content.Digest, ContentType: content.ContentType, Size: content.Size, Source: content})
		h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: principal.Actor, Outcome: repository.AuditResolved, Status: served.Status, CacheDisposition: "hit", Bytes: served.Bytes, Method: r.Method})
		return cacheServed
	}
	if errors.Is(err, ErrNegativeCache) {
		h.Runtime.RecordNegativeCacheHit()
		h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: principal.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: "hit", Method: r.Method})
		copy := member
		*lastNegative = &copy
		return cacheNegative
	}
	return cacheMiss
}

func validStagedChecksum(ctx context.Context, path string, content CachedContent) bool {
	if content.Size > 129 {
		return false
	}
	reader, _, err := content.Open(ctx)
	if err != nil {
		return false
	}
	body, readErr := io.ReadAll(io.LimitReader(reader, 130))
	closeErr := reader.Close()
	return readErr == nil && closeErr == nil && ValidChecksum(path, body)
}

func copyHeadHeaders(target, source http.Header) {
	for _, name := range []string{"Accept-Ranges", "Content-Length", "Content-Type", "Digest", "ETag", "Last-Modified"} {
		if value := source.Get(name); value != "" {
			target.Set(name, value)
		}
	}
}

func (h Handler) audit(ctx context.Context, group, path string, event AuditEvent) {
	h.Runtime.Audit(ctx, group, path, event)
}
func actor(principal Principal, authenticated bool) string {
	if authenticated && principal.Actor != "" {
		return principal.Actor
	}
	return "anonymous"
}
func auditOutcome(err error) repository.AuditOutcome {
	if errors.Is(err, repository.ErrNotFound) {
		return repository.AuditNotFound
	}
	return repository.AuditStorageError
}
func cacheDisposition(cache *Cache) string {
	if cache == nil {
		return "bypass"
	}
	return "miss"
}
