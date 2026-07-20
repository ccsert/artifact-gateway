package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/v2contract"
)

var errRawCacheMiss = errors.New("raw cache miss")
var errRawCacheNegative = errors.New("raw negative cache hit")

const defaultRawCacheTTL = 15 * time.Minute
const defaultRawNegativeCacheTTL = time.Minute

type RawClient interface {
	FetchRaw(context.Context, string, repository.Member, string, http.Header) (*http.Response, error)
}

func (c GiteaClient) FetchRaw(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	u, err := url.Parse(member.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse Raw endpoint: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + path
	u.RawPath = ""
	r, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Raw request: %w", err)
	}
	for _, name := range []string{"Accept", "Range"} {
		if v := headers.Get(name); v != "" {
			r.Header.Set(name, v)
		}
	}
	if member.Type == repository.MemberHosted {
		r.SetBasicAuth(c.Username, c.Token)
	}
	client := tracedHTTPClient(c.HTTPClient)
	// Never follow upstream redirects: a redirect can otherwise bypass the
	// configured proxy host allowlist (and may disclose hosted credentials).
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(r)
}

type rawIndex struct {
	Object, Digest, ContentType, Member, Endpoint, Repository string
	Size                                                      int64
	ExpiresAt                                                 time.Time
	Negative                                                  bool
}

type RawContent struct {
	Body                                              []byte
	Digest, ContentType, Member, Endpoint, Repository string
}

type RawCache struct {
	store            OCIObjectStore
	ttl, negativeTTL time.Duration
	allowed          map[string]struct{}
	quota            *CacheQuota
	mu               sync.Mutex
}

func NewRawCache(store OCIObjectStore, ttl, negativeTTL time.Duration, hosts []string) *RawCache {
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = struct{}{}
		}
	}
	return &RawCache{store: store, ttl: ttl, negativeTTL: negativeTTL, allowed: allowed}
}
func NewDefaultRawCache(store OCIObjectStore, hosts []string) *RawCache {
	return NewRawCache(store, defaultRawCacheTTL, defaultRawNegativeCacheTTL, hosts)
}
func (c *RawCache) WithQuota(quota *CacheQuota) *RawCache { c.quota = quota; return c }
func (c *RawCache) key(group, path, member, endpoint string) string {
	sum := sha256.Sum256([]byte(group + "\x00" + path + "\x00" + member + "\x00" + endpoint))
	return "raw/index/" + hex.EncodeToString(sum[:]) + ".json"
}
func (c *RawCache) Load(ctx context.Context, key string) (RawContent, error) {
	b, err := c.store.Get(ctx, key)
	if err != nil {
		return RawContent{}, err
	}
	var index rawIndex
	if json.Unmarshal(b, &index) != nil || !time.Now().UTC().Before(index.ExpiresAt) {
		_ = c.store.Delete(ctx, key)
		return RawContent{}, errRawCacheMiss
	}
	if index.Negative {
		return RawContent{Member: index.Member, Endpoint: index.Endpoint}, errRawCacheNegative
	}
	body, err := c.store.Get(ctx, index.Object)
	if err != nil {
		_ = c.store.Delete(ctx, key)
		return RawContent{}, errRawCacheMiss
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != index.Digest {
		_ = c.store.Delete(ctx, key)
		_ = c.store.Delete(ctx, index.Object)
		return RawContent{}, errRawCacheMiss
	}
	return RawContent{Body: body, Digest: index.Digest, ContentType: index.ContentType, Member: index.Member, Endpoint: index.Endpoint, Repository: index.Repository}, nil
}
func (c *RawCache) Store(ctx context.Context, key string, content RawContent) error {
	return c.quota.Admit(ctx, content.Repository, key, int64(len(content.Body)), func() error {
		sum := sha256.Sum256(content.Body)
		digest := hex.EncodeToString(sum[:])
		object := "raw/objects/" + digest
		if err := c.store.Put(ctx, object, content.Body); err != nil {
			return err
		}
		b, err := json.Marshal(rawIndex{Object: object, Digest: digest, ContentType: content.ContentType, Member: content.Member, Endpoint: content.Endpoint, Repository: content.Repository, Size: int64(len(content.Body)), ExpiresAt: time.Now().UTC().Add(c.ttl)})
		if err != nil {
			return err
		}
		return c.store.Put(ctx, key, b)
	})
}
func (c *RawCache) StoreNegative(ctx context.Context, key string, member repository.Member) error {
	b, err := json.Marshal(rawIndex{Member: member.Name, Endpoint: member.Endpoint, Negative: true, ExpiresAt: time.Now().UTC().Add(c.negativeTTL)})
	if err != nil {
		return err
	}
	return c.store.Put(ctx, key, b)
}
func (c *RawCache) ProxyAllowed(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	_, ok := c.allowed[strings.ToLower(u.Hostname())]
	return ok
}

type RawHandler struct {
	Store         repository.Store
	Authenticator Authenticator
	Client        RawClient
	Metrics       *Metrics
	Cache         *RawCache
}

func (h RawHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasSuffix(r.URL.EscapedPath(), "/") {
		http.NotFound(w, r)
		return
	}
	group, path, ok := parseRawPath(r.URL.EscapedPath())
	if !ok {
		http.Error(w, "invalid raw path", http.StatusBadRequest)
		return
	}
	p, ok := h.Authenticator.Authenticate(r.Header.Get("Authorization"))
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Raw"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if h.Metrics != nil {
		h.Metrics.recordRequest(group)
	}
	if !h.Authenticator.CanReadRepository(p, group) {
		h.audit(r.Context(), group, path, "", p.Actor, repository.AuditAccessDenied)
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	g, err := h.Store.GetGroup(r.Context(), group)
	if err != nil {
		h.audit(r.Context(), group, path, "", p.Actor, auditOutcome(err))
		http.NotFound(w, r)
		return
	}
	if !g.Enabled {
		h.audit(r.Context(), group, path, "", p.Actor, repository.AuditGroupDisabled)
		http.Error(w, "Raw group is disabled", http.StatusForbidden)
		return
	}
	for _, member := range prioritizeHosted(g.Members) {
		key := ""
		if h.Cache != nil {
			key = h.Cache.key(group, path, member.Name, member.Endpoint)
			content, cacheErr := h.Cache.Load(r.Context(), key)
			if cacheErr == nil {
				h.audit(r.Context(), group, path, content.Member, p.Actor, repository.AuditResolved)
				serveRaw(w, r, path, content)
				return
			}
			if errors.Is(cacheErr, errRawCacheNegative) {
				continue
			}
		}
		if member.Type == repository.MemberProxy && (h.Cache == nil || !h.Cache.ProxyAllowed(member.Endpoint)) {
			h.audit(r.Context(), group, path, member.Name, p.Actor, repository.AuditProxyDenied)
			continue
		}
		response, fetchErr := h.Client.FetchRaw(r.Context(), http.MethodGet, member, path, r.Header)
		if fetchErr != nil {
			h.audit(r.Context(), group, path, member.Name, p.Actor, repository.AuditUpstreamError)
			continue
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode >= 500 || response.StatusCode >= 300 && response.StatusCode != http.StatusNotFound {
			h.audit(r.Context(), group, path, member.Name, p.Actor, repository.AuditUpstreamError)
			continue
		}
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
			if h.Cache != nil {
				_ = h.Cache.StoreNegative(r.Context(), key, member)
			}
			h.audit(r.Context(), group, path, member.Name, p.Actor, repository.AuditNotFound)
			continue
		}
		content := RawContent{Body: body, ContentType: response.Header.Get("Content-Type"), Member: member.Name, Endpoint: member.Endpoint, Repository: group}
		if content.ContentType == "" {
			content.ContentType = "application/octet-stream"
		}
		sum := sha256.Sum256(body)
		content.Digest = hex.EncodeToString(sum[:])
		if strings.HasSuffix(path, ".sha256") || strings.HasSuffix(path, ".sha512") {
			if !validChecksum(path, body) {
				h.audit(r.Context(), group, path, member.Name, p.Actor, repository.AuditUpstreamError)
				http.Error(w, "invalid checksum sidecar", http.StatusBadGateway)
				return
			}
		}
		if h.Cache != nil {
			if err := h.Cache.Store(r.Context(), key, content); err != nil && !errors.Is(err, ErrCacheQuotaExceeded) {
				http.Error(w, "unable to cache Raw content", http.StatusInternalServerError)
				return
			}
		}
		h.audit(r.Context(), group, path, member.Name, p.Actor, repository.AuditResolved)
		serveRaw(w, r, path, content)
		return
	}
	h.audit(r.Context(), group, path, "", p.Actor, repository.AuditNotFound)
	http.NotFound(w, r)
}
func (h RawHandler) audit(ctx context.Context, group, path, member, actor string, outcome repository.AuditOutcome) {
	_ = h.Store.RecordAudit(ctx, repository.AuditRecord{GroupName: group, Repository: group, MemberName: member, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC()})
}
func parseRawPath(path string) (string, string, bool) {
	const prefix = "/raw/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	if len(parts) < 2 || parts[0] == "" {
		return "", "", false
	}
	resource, ok := v2contract.NewCanonicalResource(strings.Join(parts[1:], "/"))
	if !ok {
		return "", "", false
	}
	return parts[0], resource.String(), true
}
func validChecksum(path string, b []byte) bool {
	s := strings.TrimSuffix(string(b), "\n")
	wantLength := 64
	if strings.HasSuffix(path, ".sha512") {
		wantLength = 128
	}
	if len(s) != wantLength {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func serveRaw(w http.ResponseWriter, r *http.Request, name string, c RawContent) {
	w.Header().Set("Content-Type", c.ContentType)
	etag := `"sha256-` + c.Digest + `"`
	w.Header().Set("ETag", etag)
	digest, _ := hex.DecodeString(c.Digest)
	w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(digest))
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Header.Get("Range") != "" {
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(c.Body))
		return
	}
	w.Header().Set("Content-Length", utoa(uint64(len(c.Body))))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(c.Body)
	}
}
