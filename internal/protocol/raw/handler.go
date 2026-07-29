package raw

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		members = members[:0]
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
		if h.Cache != nil {
			release, lockErr := h.Cache.AcquireRequestLock(r.Context(), key)
			if lockErr != nil {
				http.Error(w, "unable to coordinate Raw cache fetch", http.StatusServiceUnavailable)
				return
			}
			defer release()
			cached := h.serveCached(w, r, group, path, member, p, key, &lastNegative)
			if cached == cacheServed {
				return
			}
			if cached == cacheNegative {
				continue
			}
		}
		response, fetchErr := h.Client.FetchRaw(r.Context(), http.MethodGet, member, path, nil)
		if fetchErr != nil {
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
			hadFailure = true
			continue
		}
		limit := int64(1 << 30)
		if h.Cache != nil && h.Cache.MaxObjectBytes() > 0 {
			limit = h.Cache.MaxObjectBytes()
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		_ = response.Body.Close()
		if int64(len(body)) > limit || readErr != nil || response.StatusCode >= 500 || response.StatusCode >= 300 && response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusGone {
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
			hadFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
			if h.Cache != nil {
				_ = h.Cache.StoreNegative(r.Context(), key, member)
			}
			h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: cacheDisposition(h.Cache), Method: r.Method})
			continue
		}
		content := CachedContent{Body: body, ContentType: response.Header.Get("Content-Type"), Member: member.Name, Endpoint: member.Endpoint, Repository: group, Path: path, CacheQuotaBytes: g.CacheQuotaBytes}
		if content.ContentType == "" {
			content.ContentType = "application/octet-stream"
		}
		sum := sha256.Sum256(body)
		content.Digest = hex.EncodeToString(sum[:])
		if strings.HasSuffix(path, ".sha256") || strings.HasSuffix(path, ".sha512") {
			if !ValidChecksum(path, body) {
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method, ChecksumFailure: true})
				http.Error(w, "invalid checksum sidecar", http.StatusBadGateway)
				return
			}
		}
		if h.Cache != nil {
			if err := h.Cache.Store(r.Context(), key, content); errors.Is(err, cache.ErrQuotaExceeded) {
				h.Runtime.RecordQuotaDenied()
			} else if err != nil {
				h.audit(r.Context(), group, path, AuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusInternalServerError, CacheDisposition: "bypass", Method: r.Method})
				http.Error(w, "unable to cache Raw content", http.StatusInternalServerError)
				return
			}
		}
		served := ServeContent(w, r, path, Content{Body: content.Body, Digest: content.Digest, ContentType: content.ContentType})
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
		served := ServeContent(w, r, path, Content{Body: content.Body, Digest: content.Digest, ContentType: content.ContentType})
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
