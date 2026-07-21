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
	"net"
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

var rawProxyLookupIP = net.DefaultResolver.LookupIP
var rawProxyDialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, address)
}

const defaultRawCacheTTL = 15 * time.Minute
const defaultRawNegativeCacheTTL = time.Minute
const defaultRawMaxObjectBytes = int64(1 << 30)

type RawClient interface {
	FetchRaw(context.Context, string, repository.Member, string, http.Header) (*http.Response, error)
}

func (c GiteaClient) FetchRaw(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	client := c.HTTPClient
	if member.Type == repository.MemberProxy {
		u, ips, err := resolveRawProxyEndpoint(ctx, member.Endpoint)
		if err != nil {
			return nil, err
		}
		client, err = rawProxyHTTPClient(client, u.Hostname(), u.Port(), ips)
		if err != nil {
			return nil, err
		}
	}
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
	// Raw has one cached file representation. Range and content negotiation are
	// applied locally after the complete canonical representation is fetched.
	if member.Type == repository.MemberHosted {
		r.SetBasicAuth(c.Username, c.Token)
	}
	client = tracedHTTPClient(client)
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
	maxObjectBytes   int64
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
	return &RawCache{store: store, ttl: ttl, negativeTTL: negativeTTL, maxObjectBytes: defaultRawMaxObjectBytes, allowed: allowed}
}
func NewDefaultRawCache(store OCIObjectStore, hosts []string) *RawCache {
	return NewRawCache(store, defaultRawCacheTTL, defaultRawNegativeCacheTTL, hosts)
}
func (c *RawCache) WithQuota(quota *CacheQuota) *RawCache { c.quota = quota; return c }
func (c *RawCache) WithMaxObjectBytes(limit int64) *RawCache {
	if limit > 0 {
		c.maxObjectBytes = limit
	}
	return c
}
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
func (c *RawCache) Invalidate(ctx context.Context, key string) { _ = c.store.Delete(ctx, key) }
func (c *RawCache) CollectGarbage(ctx context.Context) error {
	keys, err := c.store.List(ctx, "raw/index/")
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	now := time.Now().UTC()
	for _, key := range keys {
		encoded, err := c.store.Get(ctx, key)
		if err != nil {
			continue
		}
		var index rawIndex
		if json.Unmarshal(encoded, &index) != nil || !now.Before(index.ExpiresAt) {
			_ = c.store.Delete(ctx, key)
			continue
		}
		if index.Object != "" {
			referenced[index.Object] = true
		}
	}
	objects, err := c.store.List(ctx, "raw/objects/")
	if err != nil {
		return err
	}
	for _, object := range objects {
		if !referenced[object] {
			_ = c.store.Delete(ctx, object)
		}
	}
	return nil
}
func (c *RawCache) ProxyAllowed(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && privateAddress(ip) {
		return false
	}
	_, ok := c.allowed[strings.ToLower(u.Hostname())]
	return ok
}

func safeRawProxyEndpoint(ctx context.Context, endpoint string) error {
	_, _, err := resolveRawProxyEndpoint(ctx, endpoint)
	return err
}

func resolveRawProxyEndpoint(ctx context.Context, endpoint string) (*url.URL, []net.IP, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return nil, nil, errors.New("Raw proxy endpoint is not a valid HTTPS URL")
	}
	ips, err := rawProxyLookupIP(ctx, "ip", u.Hostname())
	if err != nil || len(ips) == 0 {
		return nil, nil, fmt.Errorf("resolve Raw proxy endpoint: %w", err)
	}
	for _, ip := range ips {
		if privateAddress(ip) {
			return nil, nil, errors.New("Raw proxy endpoint resolves to a private address")
		}
	}
	return u, ips, nil
}

// rawProxyHTTPClient pins the TCP connection to the address that passed the
// private-network check, preventing a second DNS resolution from rebinding it.
func rawProxyHTTPClient(client *http.Client, hostname, port string, ips []net.IP) (*http.Client, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	base, ok := transport.(*http.Transport)
	if !ok {
		return nil, errors.New("Raw proxy HTTP client must use *http.Transport")
	}
	if base.DialTLSContext != nil || base.DialTLS != nil {
		return nil, errors.New("Raw proxy HTTP client must not override TLS dialing")
	}
	copy := *client
	pinnedTransport := base.Clone()
	pinnedTransport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		requestHost, requestPort, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(requestHost, hostname) {
			return nil, errors.New("Raw proxy dial target changed")
		}
		if port != "" {
			requestPort = port
		}
		var lastErr error
		for _, ip := range ips {
			connection, err := rawProxyDialContext(ctx, network, net.JoinHostPort(ip.String(), requestPort))
			if err == nil {
				return connection, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	copy.Transport = pinnedTransport
	return &copy, nil
}

func privateAddress(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()
}

type RawHandler struct {
	Store         repository.Store
	Authenticator Authenticator
	Client        RawClient
	Metrics       *Metrics
	Cache         *RawCache
}

type rawAuditEvent struct {
	Member           repository.Member
	Actor            string
	Outcome          repository.AuditOutcome
	Status           int
	CacheDisposition string
	Bytes            int64
	Method           string
}

func (h RawHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.EscapedPath(), "/") {
		http.NotFound(w, r)
		return
	}
	group, path, ok := parseRawPath(r.URL.EscapedPath())
	if !ok {
		http.Error(w, "invalid raw path", http.StatusBadRequest)
		return
	}
	p, authenticated := h.Authenticator.Authenticate(r.Header.Get("Authorization"))
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.audit(r.Context(), group, path, rawAuditEvent{Actor: rawAuditActor(p, authenticated), Outcome: repository.AuditAccessDenied, Status: http.StatusMethodNotAllowed, CacheDisposition: "bypass", Method: r.Method})
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !authenticated && h.anonymousRawAllowed(r.Context(), group) {
		p = Principal{Actor: "anonymous"}
		authenticated = true
	}
	if !authenticated {
		h.audit(r.Context(), group, path, rawAuditEvent{Actor: "anonymous", Outcome: repository.AuditAccessDenied, Status: http.StatusUnauthorized, CacheDisposition: "bypass", Method: r.Method})
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Raw"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if h.Metrics != nil {
		h.Metrics.recordRequest(group)
		if p.Actor == "anonymous" {
			h.Metrics.recordAnonymousRead()
		}
	}
	if p.Actor != "anonymous" && !h.Authenticator.CanReadRepository(p, group) {
		h.audit(r.Context(), group, path, rawAuditEvent{Actor: p.Actor, Outcome: repository.AuditAccessDenied, Status: http.StatusForbidden, CacheDisposition: "bypass", Method: r.Method})
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	g, err := h.Store.GetGroup(r.Context(), group)
	if err != nil {
		h.audit(r.Context(), group, path, rawAuditEvent{Actor: p.Actor, Outcome: auditOutcome(err), Status: http.StatusNotFound, CacheDisposition: "bypass", Method: r.Method})
		http.NotFound(w, r)
		return
	}
	if !g.Enabled {
		h.audit(r.Context(), group, path, rawAuditEvent{Actor: p.Actor, Outcome: repository.AuditGroupDisabled, Status: http.StatusForbidden, CacheDisposition: "bypass", Method: r.Method})
		http.Error(w, "Raw group is disabled", http.StatusForbidden)
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
	hadUpstreamFailure := false
	hadProxyDenied := false
	var lastNegative *repository.Member
	for _, member := range prioritizeHosted(members) {
		key := ""
		if h.Cache != nil {
			key = h.Cache.key(group, path, member.Name, member.Endpoint)
			content, cacheErr := h.Cache.Load(r.Context(), key)
			if cacheErr == nil {
				served := serveRaw(w, r, path, content)
				h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditResolved, Status: served.Status, CacheDisposition: "hit", Bytes: served.Bytes, Method: r.Method})
				return
			}
			if errors.Is(cacheErr, errRawCacheNegative) {
				h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: "hit", Method: r.Method})
				negativeMember := member
				lastNegative = &negativeMember
				continue
			}
		}
		if member.Type == repository.MemberProxy && !rawProxyAllowed(member) {
			h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditProxyDenied, Status: http.StatusForbidden, CacheDisposition: "bypass", Method: r.Method})
			hadProxyDenied = true
			continue
		}
		response, fetchErr := h.Client.FetchRaw(r.Context(), http.MethodGet, member, path, nil)
		if fetchErr != nil {
			h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
			hadUpstreamFailure = true
			continue
		}
		limit := defaultRawMaxObjectBytes
		if h.Cache != nil && h.Cache.maxObjectBytes > 0 {
			limit = h.Cache.maxObjectBytes
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		_ = response.Body.Close()
		if int64(len(body)) > limit {
			h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
			hadUpstreamFailure = true
			continue
		}
		if readErr != nil || response.StatusCode >= 500 || response.StatusCode >= 300 && response.StatusCode != http.StatusNotFound && response.StatusCode != http.StatusGone {
			h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
			hadUpstreamFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
			if h.Cache != nil {
				_ = h.Cache.StoreNegative(r.Context(), key, member)
			}
			h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: rawCacheDisposition(h.Cache), Method: r.Method})
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
				h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusBadGateway, CacheDisposition: "bypass", Method: r.Method})
				http.Error(w, "invalid checksum sidecar", http.StatusBadGateway)
				return
			}
		}
		if h.Cache != nil {
			if err := h.Cache.Store(r.Context(), key, content); err != nil && !errors.Is(err, ErrCacheQuotaExceeded) {
				h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditUpstreamError, Status: http.StatusInternalServerError, CacheDisposition: "bypass", Method: r.Method})
				http.Error(w, "unable to cache Raw content", http.StatusInternalServerError)
				return
			}
		}
		served := serveRaw(w, r, path, content)
		h.audit(r.Context(), group, path, rawAuditEvent{Member: member, Actor: p.Actor, Outcome: repository.AuditResolved, Status: served.Status, CacheDisposition: rawCacheDisposition(h.Cache), Bytes: served.Bytes, Method: r.Method})
		return
	}
	if hadUpstreamFailure {
		http.Error(w, "upstream repository unavailable", http.StatusBadGateway)
		return
	}
	if hadProxyDenied {
		http.Error(w, "upstream repository is not allowed", http.StatusForbidden)
		return
	}
	if lastNegative != nil {
		h.audit(r.Context(), group, path, rawAuditEvent{Member: *lastNegative, Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: "hit", Method: r.Method})
	} else {
		h.audit(r.Context(), group, path, rawAuditEvent{Actor: p.Actor, Outcome: repository.AuditNotFound, Status: http.StatusNotFound, CacheDisposition: rawCacheDisposition(h.Cache), Method: r.Method})
	}
	http.NotFound(w, r)
}

func rawAuditActor(principal Principal, authenticated bool) string {
	if authenticated && principal.Actor != "" {
		return principal.Actor
	}
	return "anonymous"
}

func rawProxyAllowed(member repository.Member) bool {
	u, err := url.Parse(member.Endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && privateAddress(ip) {
		return false
	}
	for _, host := range member.AllowedHosts {
		if strings.EqualFold(strings.TrimSpace(host), u.Hostname()) {
			return true
		}
	}
	return false
}
func (h RawHandler) audit(ctx context.Context, group, path string, event rawAuditEvent) {
	if event.Actor == "" {
		event.Actor = "anonymous"
	}
	upstreamHost := ""
	if endpoint, err := url.Parse(event.Member.Endpoint); err == nil {
		upstreamHost = endpoint.Hostname()
	}
	_ = h.Store.RecordAudit(ctx, repository.AuditRecord{
		GroupName: group, Repository: group, MemberName: event.Member.Name, Actor: event.Actor, Outcome: event.Outcome, OccurredAt: time.Now().UTC(),
		Format: "raw", Resource: path, Representation: "body", MemberType: string(event.Member.Type), UpstreamHost: upstreamHost,
		Operation: strings.ToLower(event.Method), Status: event.Status, CacheDisposition: event.CacheDisposition, Bytes: event.Bytes,
	})
}

func rawCacheDisposition(cache *RawCache) string {
	if cache == nil {
		return "bypass"
	}
	return "miss"
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

type rawStatusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *rawStatusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *rawStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += int64(n)
	return n, err
}

type rawServeResult struct {
	Status int
	Bytes  int64
}

func (w *rawStatusWriter) result() rawServeResult {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return rawServeResult{Status: status}
	}
	return rawServeResult{Status: status, Bytes: w.bytes}
}

func serveRaw(w http.ResponseWriter, r *http.Request, name string, c RawContent) rawServeResult {
	statusWriter := &rawStatusWriter{ResponseWriter: w}
	w.Header().Set("Content-Type", c.ContentType)
	etag := `"sha256-` + c.Digest + `"`
	w.Header().Set("ETag", etag)
	digest, _ := hex.DecodeString(c.Digest)
	w.Header().Set("Digest", "sha-256="+base64.StdEncoding.EncodeToString(digest))
	if r.Header.Get("If-None-Match") == etag {
		statusWriter.WriteHeader(http.StatusNotModified)
		return statusWriter.result()
	}
	if r.Header.Get("Range") != "" {
		http.ServeContent(statusWriter, r, name, time.Time{}, bytes.NewReader(c.Body))
		return statusWriter.result()
	}
	w.Header().Set("Content-Length", utoa(uint64(len(c.Body))))
	statusWriter.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = statusWriter.Write(c.Body)
	}
	return statusWriter.result()
}
