package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// ConanClient is deliberately narrower than a generic HTTP client: client
// credentials are applied only to Hosted members and never forwarded to Proxy.
type ConanClient interface {
	FetchConan(context.Context, string, repository.Member, string, http.Header) (*http.Response, error)
}

func (c GiteaClient) FetchConan(ctx context.Context, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	if member.Type == repository.MemberProxy {
		proxyURL, ips, err := resolveRawProxyEndpoint(ctx, member.Endpoint)
		if err != nil {
			return nil, err
		}
		client, err := rawProxyHTTPClient(c.HTTPClient, proxyURL.Hostname(), proxyURL.Port(), ips)
		if err != nil {
			return nil, err
		}
		return c.fetchConan(ctx, client, method, member, path, headers)
	}
	return c.fetchConan(ctx, c.HTTPClient, method, member, path, headers)
}

func (c GiteaClient) fetchConan(ctx context.Context, client *http.Client, method string, member repository.Member, path string, headers http.Header) (*http.Response, error) {
	u, err := url.Parse(strings.TrimRight(member.Endpoint, "/") + "/conans/" + path)
	if err != nil {
		return nil, fmt.Errorf("parse Conan endpoint: %w", err)
	}
	r, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if accept := headers.Get("Accept"); accept != "" {
		r.Header.Set("Accept", accept)
	}
	if member.Type == repository.MemberHosted {
		r.SetBasicAuth(c.Username, c.Token)
	}
	client = tracedHTTPClient(client)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(r)
}

type conanCacheEntry struct {
	body                          []byte
	contentType, member, endpoint string
	cacheDisposition              string
	status                        int
	expires                       time.Time
}

type conanCacheIndex struct {
	Object, Digest, ContentType, Member, Endpoint, Repository string
	Group, Path, Representation                               string
	Size                                                      int64
	Status                                                    int
	ExpiresAt                                                 time.Time
	Negative                                                  bool
}
type ConanCache struct {
	objectStore    OCIObjectStore
	quota          *CacheQuota
	maxObjectBytes int64
	coordinator    OCICacheCoordinator
	publicationMu  sync.Mutex
}

func NewConanCache(hosts []string) *ConanCache {
	return NewConanCacheWithStore(NewMemoryOCIObjectStore(), hosts)
}
func NewConanCacheWithStore(store OCIObjectStore, _ []string) *ConanCache {
	return &ConanCache{objectStore: store, maxObjectBytes: defaultRawMaxObjectBytes}
}
func NewDefaultConanCache(store OCIObjectStore, hosts []string) *ConanCache {
	return NewConanCacheWithStore(store, hosts)
}
func (c *ConanCache) WithQuota(quota *CacheQuota) *ConanCache { c.quota = quota; return c }
func (c *ConanCache) WithCoordinator(coordinator OCICacheCoordinator) *ConanCache {
	c.coordinator = coordinator
	return c
}
func (c *ConanCache) WithMaxObjectBytes(limit int64) *ConanCache {
	if limit > 0 {
		c.maxObjectBytes = limit
	}
	return c
}
func (c *ConanCache) load(ctx context.Context, key string) (conanCacheEntry, bool) {
	encoded, err := c.objectStore.Get(ctx, key)
	if err != nil {
		return conanCacheEntry{}, false
	}
	var index conanCacheIndex
	if json.Unmarshal(encoded, &index) != nil || !time.Now().UTC().Before(index.ExpiresAt) {
		_ = c.objectStore.Delete(ctx, key)
		return conanCacheEntry{}, false
	}
	if index.Negative {
		return conanCacheEntry{status: http.StatusNotFound, member: index.Member, endpoint: index.Endpoint}, true
	}
	body, err := c.objectStore.Get(ctx, index.Object)
	if err != nil {
		_ = c.objectStore.Delete(ctx, key)
		return conanCacheEntry{}, false
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != index.Digest {
		_ = c.objectStore.Delete(ctx, key)
		return conanCacheEntry{}, false
	}
	return conanCacheEntry{body: body, contentType: index.ContentType, member: index.Member, endpoint: index.Endpoint, status: index.Status}, true
}
func (c *ConanCache) store(ctx context.Context, key string, e conanCacheEntry, repositoryName string, quotaBytes int64, ttl time.Duration, identity ...string) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		return c.quota.AdmitConanWithLimit(workCtx, repositoryName, key, int64(len(e.body)), quotaBytes, func() error {
			sum := sha256.Sum256(e.body)
			digest := hex.EncodeToString(sum[:])
			object := "conan/objects/" + digest
			if !e.statusIsNegative() {
				if err := c.objectStore.Put(workCtx, object, e.body); err != nil {
					return err
				}
			}
			index := conanCacheIndex{Object: object, Digest: digest, ContentType: e.contentType, Member: e.member, Endpoint: e.endpoint, Repository: repositoryName, Size: int64(len(e.body)), Status: e.status, ExpiresAt: time.Now().UTC().Add(ttl), Negative: e.status == http.StatusNotFound}
			if len(identity) == 3 {
				index.Group, index.Path, index.Representation = identity[0], identity[1], identity[2]
			}
			encoded, err := json.Marshal(index)
			if err != nil {
				return err
			}
			return c.objectStore.Put(workCtx, key, encoded)
		})
	})
}
func (e conanCacheEntry) statusIsNegative() bool { return e.status == http.StatusNotFound }
func (c *ConanCache) key(group, path string, member repository.Member, representation ...string) string {
	value := ""
	if len(representation) > 0 {
		value = representation[0]
	}
	sum := sha256.Sum256([]byte(group + "\x00" + path + "\x00" + member.Name + "\x00" + member.Endpoint + "\x00" + value))
	return "conan/index/" + hex.EncodeToString(sum[:]) + ".json"
}
func (c *ConanCache) Invalidate(ctx context.Context, group, path string, member repository.Member) {
	_ = c.withPublicationLock(ctx, func(workCtx context.Context) error {
		// Remove indexes written before representation was recorded in their value.
		_ = c.objectStore.Delete(workCtx, c.key(group, path, member))
		keys, err := c.objectStore.List(workCtx, "conan/index/")
		if err != nil {
			return err
		}
		for _, key := range keys {
			encoded, err := c.objectStore.Get(workCtx, key)
			if err != nil {
				continue
			}
			var index conanCacheIndex
			if json.Unmarshal(encoded, &index) == nil && index.Group == group && index.Path == path && index.Member == member.Name && index.Endpoint == member.Endpoint {
				_ = c.objectStore.Delete(workCtx, key)
			}
		}
		return nil
	})
}
func (c *ConanCache) CollectGarbage(ctx context.Context) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error { return c.collectGarbage(workCtx) })
}
func (c *ConanCache) collectGarbage(ctx context.Context) error {
	keys, err := c.objectStore.List(ctx, "conan/index/")
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for _, key := range keys {
		encoded, err := c.objectStore.Get(ctx, key)
		if err != nil {
			continue
		}
		var index conanCacheIndex
		if json.Unmarshal(encoded, &index) != nil || !time.Now().UTC().Before(index.ExpiresAt) {
			_ = c.objectStore.Delete(ctx, key)
			continue
		}
		if index.Object != "" {
			referenced[index.Object] = true
		}
	}
	objects, err := c.objectStore.List(ctx, "conan/objects/")
	if err != nil {
		return err
	}
	for _, object := range objects {
		if !referenced[object] {
			_ = c.objectStore.Delete(ctx, object)
		}
	}
	return nil
}
func (c *ConanCache) withPublicationLock(ctx context.Context, work func(context.Context) error) error {
	c.publicationMu.Lock()
	defer c.publicationMu.Unlock()
	if c.coordinator == nil {
		return work(ctx)
	}
	for {
		owner, acquired, err := c.coordinator.Acquire(ctx, "conan-publication", rawDistributedLockLease)
		if err != nil {
			return err
		}
		if !acquired {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = c.coordinator.Release(releaseCtx, "conan-publication", owner)
		}()
		workCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		failed := make(chan struct{})
		go c.renewConanLock(workCtx, owner, failed, cancel)
		err = work(workCtx)
		select {
		case <-failed:
			return errors.New("Conan distributed publication lock renewal failed")
		default:
			return err
		}
	}
}
func (c *ConanCache) renewConanLock(ctx context.Context, owner string, failed chan<- struct{}, cancel context.CancelFunc) {
	ticker := time.NewTicker(rawDistributedLockRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := c.coordinator.Renew(ctx, "conan-publication", owner, rawDistributedLockLease)
			if err != nil || !ok {
				close(failed)
				cancel()
				return
			}
		}
	}
}
func (c *ConanCache) proxyAllowed(member repository.Member) bool {
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

type ConanHandler struct {
	Store         repository.ConanStore
	Authenticator Authenticator
	Client        ConanClient
	Cache         *ConanCache
	Metrics       *Metrics
}

func (h ConanHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r = r.WithContext(withRawAuditCorrelation(r.Context(), r.Header.Get("X-Request-ID")))
	r = r.WithContext(withConanAuditState(r.Context(), r.Method, conanRepresentation(r.Header), conanCacheDisposition(h.Cache)))
	if h.Metrics != nil {
		h.Metrics.recordConanRequest(r.Method)
	}
	if group, ok := parseConanPing(r.Method, r.URL.Path); ok {
		if h.anonymousConanAllowed(r.Context(), group) {
			w.Header().Set("X-Conan-Server-Capabilities", "revisions")
			h.audit(withConanAuditStatus(r.Context(), http.StatusOK), group, "", "", "anonymous", repository.AuditResolved)
			w.WriteHeader(http.StatusOK)
			return
		}
		principal, authenticated := h.authenticate(r)
		configured, err := h.Store.GetConanGroup(r.Context(), group)
		if err != nil || !configured.Enabled {
			h.audit(withConanAuditStatus(r.Context(), http.StatusNotFound), group, "", "", principal.Actor, auditOutcome(err))
			http.NotFound(w, r)
			return
		}
		if authenticated && h.Authenticator.CanReadMavenRepository(principal, group) {
			w.Header().Set("X-Conan-Server-Capabilities", "revisions")
			h.audit(withConanAuditStatus(r.Context(), http.StatusOK), group, "", "", principal.Actor, repository.AuditResolved)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		actor := "anonymous"
		status := http.StatusUnauthorized
		if authenticated {
			actor = principal.Actor
			status = http.StatusForbidden
		}
		h.audit(withConanAuditStatus(r.Context(), status), group, "", "", actor, repository.AuditAccessDenied)
		if authenticated {
			http.Error(w, "repository read permission required", status)
			return
		}
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if group, ok := parseConanAuthenticate(r.Method, r.URL.Path); ok {
		principal, authenticated := h.authenticate(r)
		configured, err := h.Store.GetConanGroup(r.Context(), group)
		if err != nil || !configured.Enabled {
			h.audit(withConanAuditStatus(r.Context(), http.StatusNotFound), group, "", "", principal.Actor, auditOutcome(err))
			http.NotFound(w, r)
			return
		}
		if !authenticated {
			w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
			h.audit(withConanAuditStatus(r.Context(), http.StatusUnauthorized), group, "", "", "anonymous", repository.AuditAccessDenied)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if !h.Authenticator.CanReadMavenRepository(principal, group) {
			h.audit(withConanAuditStatus(r.Context(), http.StatusForbidden), group, "", "", principal.Actor, repository.AuditAccessDenied)
			http.Error(w, "repository read permission required", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		h.audit(withConanAuditStatus(r.Context(), http.StatusOK), group, "", "", principal.Actor, repository.AuditResolved)
		_, _ = w.Write([]byte(`{"user_name":` + strconv.Quote(principal.Actor) + `}`))
		return
	}
	group, path, kind, file, ok := parseConanPath(r.Method, r.URL.EscapedPath())
	if !ok {
		if malformedConanPath(r.URL.EscapedPath()) {
			h.audit(withConanAuditStatus(r.Context(), http.StatusBadRequest), "", "", "", "anonymous", repository.AuditUpstreamError)
			http.Error(w, "invalid Conan path", http.StatusBadRequest)
			return
		}
		h.audit(withConanAuditStatus(r.Context(), http.StatusNotFound), "", "", "", "anonymous", repository.AuditNotFound)
		http.NotFound(w, r)
		return
	}
	p, authenticated := h.authenticate(r)
	if !authenticated && h.anonymousConanAllowed(r.Context(), group) {
		p = Principal{Actor: "anonymous"}
		authenticated = true
	}
	if !authenticated {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		h.audit(withConanAuditStatus(r.Context(), http.StatusUnauthorized), group, path, "", "anonymous", repository.AuditAccessDenied)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if p.Actor != "anonymous" && !h.Authenticator.CanReadMavenRepository(p, group) {
		h.audit(r.Context(), group, path, "", p.Actor, repository.AuditAccessDenied)
		http.Error(w, "repository read permission required", http.StatusForbidden)
		return
	}
	g, err := h.Store.GetConanGroup(r.Context(), group)
	if err != nil || !g.Enabled {
		h.audit(r.Context(), group, path, "", p.Actor, auditOutcome(err))
		http.NotFound(w, r)
		return
	}
	if h.Metrics != nil {
		h.Metrics.recordRequest(group)
		if p.Actor == "anonymous" {
			h.Metrics.recordAnonymousRead()
		}
	}
	content, status, err := h.resolve(r.Context(), g, path, kind, r.Header, p)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if file != "" && !h.verifyFile(r.Context(), g, path, file, content, p) {
		h.audit(withConanAuditChecksum(r.Context()), group, path, content.member, p.Actor, repository.AuditUpstreamError)
		http.Error(w, "Conan file checksum mismatch", http.StatusBadGateway)
		return
	}
	if file != "" && h.Cache != nil {
		representation := conanRepresentation(r.Header)
		if err := h.Cache.store(r.Context(), h.Cache.key(group, path, repository.Member{Name: content.member, Endpoint: content.endpoint}, representation), content, group, g.CacheQuotaBytes, 15*time.Minute, group, path, representation); errors.Is(err, ErrCacheQuotaExceeded) {
			if h.Metrics != nil {
				h.Metrics.cacheQuotaDenied.Add(1)
				h.Metrics.conanCacheQuotaDenied.Add(1)
			}
		} else if err != nil {
			h.audit(r.Context(), group, path, content.member, p.Actor, repository.AuditUpstreamError)
			http.Error(w, "unable to cache Conan content", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", content.contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(content.body)))
	servedBytes := int64(0)
	if r.Method == http.MethodGet {
		n, _ := w.Write(content.body)
		servedBytes = int64(n)
	}
	h.audit(withConanAuditBytes(withConanAuditStatus(withConanAuditDisposition(r.Context(), content.cacheDisposition), content.status), servedBytes), group, path, content.member, p.Actor, repository.AuditResolved)
}

func (h ConanHandler) authenticate(request *http.Request) (Principal, bool) {
	if principal, ok := h.Authenticator.Authenticate(request.Header.Get("Authorization")); ok {
		return principal, true
	}
	username, password, ok := request.BasicAuth()
	if !ok || username == "" || !tokenMatches(password, h.Authenticator.ResolverToken) {
		return Principal{}, false
	}
	return h.Authenticator.principal(username), true
}

func (h ConanHandler) resolve(ctx context.Context, group repository.Group, path, kind string, headers http.Header, principal Principal) (conanCacheEntry, int, error) {
	actor := principal.Actor
	metadata := kind == "metadata"
	hadFailure, denied := false, false
	members := group.Members
	if actor == "anonymous" {
		members = make([]repository.Member, 0, len(group.Members))
		for _, member := range group.Members {
			if member.Anonymous {
				members = append(members, member)
			}
		}
	}
	for _, member := range prioritizeHosted(members) {
		if actor != "anonymous" && !h.Authenticator.CanReadRepository(principal, member.Name) {
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditAccessDenied)
			return conanCacheEntry{}, http.StatusForbidden, errors.New("repository read permission required")
		}
		key := ""
		representation := conanRepresentation(headers)
		if h.Cache != nil {
			key = h.Cache.key(group.Name, path, member, representation)
			if e, ok := h.Cache.load(ctx, key); ok {
				if e.status == http.StatusNotFound {
					if h.Metrics != nil {
						h.Metrics.recordConanNegativeCacheHit()
					}
					h.audit(withConanAuditDisposition(ctx, "hit"), group.Name, path, member.Name, actor, repository.AuditNotFound)
					continue
				}
				if h.Metrics != nil {
					h.Metrics.recordConanCacheHit()
				}
				e.cacheDisposition = "hit"
				return e, http.StatusOK, nil
			}
			if h.Metrics != nil {
				h.Metrics.recordConanCacheMiss()
			}
		}
		if member.Type == repository.MemberProxy && (h.Cache == nil || !h.Cache.proxyAllowed(member)) {
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditProxyDenied)
			denied = true
			continue
		}
		response, err := h.Client.FetchConan(ctx, http.MethodGet, member, path, headers)
		if err != nil {
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			hadFailure = true
			continue
		}
		limit := defaultRawMaxObjectBytes
		if h.Cache != nil && h.Cache.maxObjectBytes > 0 {
			limit = h.Cache.maxObjectBytes
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
		_ = response.Body.Close()
		if readErr != nil || int64(len(body)) > limit || response.StatusCode >= 500 || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
			h.audit(withConanAuditStatus(ctx, response.StatusCode), group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			hadFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			if h.Cache != nil {
				_ = h.Cache.store(ctx, key, conanCacheEntry{status: http.StatusNotFound, member: member.Name, endpoint: member.Endpoint}, group.Name, group.CacheQuotaBytes, time.Minute, group.Name, path, representation)
			}
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditNotFound)
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			h.audit(withConanAuditStatus(ctx, response.StatusCode), group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			hadFailure = true
			continue
		}
		if metadata && !validConanMetadata(path, body) {
			h.audit(withConanAuditStatus(ctx, response.StatusCode), group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			return conanCacheEntry{}, http.StatusBadRequest, errors.New("invalid Conan metadata")
		}
		e := conanCacheEntry{body: body, contentType: response.Header.Get("Content-Type"), member: member.Name, endpoint: member.Endpoint, status: response.StatusCode, cacheDisposition: conanCacheDisposition(h.Cache)}
		if e.contentType == "" {
			if metadata {
				e.contentType = "application/json"
			} else {
				e.contentType = "application/octet-stream"
			}
		}
		if h.Cache != nil && kind != "file" {
			ttl := 15 * time.Minute
			if metadata {
				ttl = time.Minute
			}
			if err := h.Cache.store(ctx, key, e, group.Name, group.CacheQuotaBytes, ttl, group.Name, path, representation); errors.Is(err, ErrCacheQuotaExceeded) {
				if h.Metrics != nil {
					h.Metrics.cacheQuotaDenied.Add(1)
					h.Metrics.conanCacheQuotaDenied.Add(1)
				}
			} else if err != nil {
				h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditUpstreamError)
				return conanCacheEntry{}, http.StatusInternalServerError, errors.New("unable to cache Conan content")
			}
		}
		return e, http.StatusOK, nil
	}
	if hadFailure {
		return conanCacheEntry{}, http.StatusBadGateway, errors.New("upstream repository unavailable")
	}
	if denied {
		return conanCacheEntry{}, http.StatusForbidden, errors.New("upstream repository is not allowed")
	}
	return conanCacheEntry{}, http.StatusNotFound, errors.New("Conan resource not found")
}
func conanRepresentation(headers http.Header) string { return strings.TrimSpace(headers.Get("Accept")) }

func (h ConanHandler) verifyFile(ctx context.Context, group repository.Group, path, file string, content conanCacheEntry, principal Principal) bool {
	metadataPath := path[:strings.LastIndex(path, "/files/")] + "/files"
	metadata, status, err := h.resolve(ctx, group, metadataPath, "metadata", http.Header{}, principal)
	if err != nil || status != http.StatusOK {
		return false
	}
	var value struct {
		Files map[string]struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"files"`
	}
	if json.Unmarshal(metadata.body, &value) != nil {
		return false
	}
	want, found := value.Files[file]
	sum := sha256.Sum256(content.body)
	return found && want.Size == int64(len(content.body)) && want.SHA256 == hex.EncodeToString(sum[:])
}

type conanAuditState struct {
	method, representation, cacheDisposition string
	bytes                                    int64
	checksumFailure                          bool
	status                                   int
}
type conanAuditStateKey struct{}

func withConanAuditState(ctx context.Context, method, representation, disposition string) context.Context {
	return context.WithValue(ctx, conanAuditStateKey{}, conanAuditState{method: strings.ToLower(method), representation: representation, cacheDisposition: disposition})
}
func withConanAuditDisposition(ctx context.Context, disposition string) context.Context {
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	state.cacheDisposition = disposition
	return context.WithValue(ctx, conanAuditStateKey{}, state)
}
func withConanAuditBytes(ctx context.Context, bytes int64) context.Context {
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	state.bytes = bytes
	return context.WithValue(ctx, conanAuditStateKey{}, state)
}
func withConanAuditChecksum(ctx context.Context) context.Context {
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	state.checksumFailure = true
	return context.WithValue(ctx, conanAuditStateKey{}, state)
}
func withConanAuditStatus(ctx context.Context, status int) context.Context {
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	state.status = status
	return context.WithValue(ctx, conanAuditStateKey{}, state)
}
func conanCacheDisposition(cache *ConanCache) string {
	if cache == nil {
		return "bypass"
	}
	return "miss"
}

func (h ConanHandler) audit(ctx context.Context, group, path, member, actor string, outcome repository.AuditOutcome) {
	if actor == "" {
		actor = "anonymous"
	}
	state, _ := ctx.Value(conanAuditStateKey{}).(conanAuditState)
	selected := repository.Member{Name: member}
	if configured, err := h.Store.GetConanGroup(ctx, group); err == nil {
		for _, candidate := range configured.Members {
			if candidate.Name == member {
				selected = candidate
				break
			}
		}
	}
	upstreamHost := ""
	if endpoint, err := url.Parse(selected.Endpoint); err == nil {
		upstreamHost = endpoint.Hostname()
	}
	status := http.StatusNotFound
	switch outcome {
	case repository.AuditResolved:
		status = http.StatusOK
	case repository.AuditAccessDenied, repository.AuditProxyDenied, repository.AuditGroupDisabled:
		status = http.StatusForbidden
	case repository.AuditUpstreamError:
		status = http.StatusBadGateway
	}
	if state.status != 0 {
		status = state.status
	}
	bytes := state.bytes
	_ = h.Store.RecordAudit(ctx, repository.AuditRecord{GroupName: group, Repository: path, MemberName: member, Actor: actor, Outcome: outcome, OccurredAt: time.Now().UTC(), Format: "conan", Resource: path, Representation: state.representation, MemberType: string(selected.Type), UpstreamHost: upstreamHost, Operation: state.method, Status: status, CacheDisposition: state.cacheDisposition, Bytes: bytes, RequestID: rawAuditRequestID(ctx), TraceID: rawAuditTraceID(ctx)})
	if h.Metrics != nil {
		h.Metrics.recordConanAudit(outcome, state.bytes, state.checksumFailure)
	}
}

func parseConanPath(method, raw string) (group, path, kind, file string, ok bool) {
	if method != http.MethodGet && method != http.MethodHead {
		return
	}
	parts := strings.Split(strings.TrimPrefix(raw, "/"), "/")
	if len(parts) < 8 || parts[0] != "conan" {
		return
	}
	var rest []string
	if parts[1] == "v2" && parts[3] == "conans" {
		group, rest = parts[2], parts[4:]
	} else if parts[2] == "v2" && parts[3] == "conans" {
		group, rest = parts[1], parts[4:]
	} else {
		return
	}
	for _, part := range append([]string{group}, rest...) {
		if !validConanSegment(part) {
			return
		}
	}
	if len(rest) < 5 {
		return
	}
	// recipe: n/v/u/c/revisions; package appends rrev/packages/id/revisions.
	if len(rest) == 5 && rest[4] == "revisions" {
		path = strings.Join(rest, "/")
		kind = "metadata"
		ok = true
		return
	}
	if len(rest) >= 6 && rest[4] == "revisions" {
		if len(rest) == 7 && rest[6] == "search" {
			path = strings.Join(rest, "/")
			kind = "metadata"
			ok = true
			return
		}
		if len(rest) == 7 && rest[6] == "files" {
			path = strings.Join(rest, "/")
			kind = "metadata"
			ok = true
			return
		}
		if len(rest) == 8 && rest[6] == "files" {
			path = strings.Join(rest, "/")
			kind = "file"
			file = rest[7]
			ok = true
			return
		}
		if len(rest) == 9 && rest[6] == "packages" && rest[8] == "revisions" {
			path = strings.Join(rest, "/")
			kind = "metadata"
			ok = true
			return
		}
		if len(rest) == 9 && rest[6] == "packages" && rest[8] == "latest" {
			path = strings.Join(rest, "/")
			// Conan 2 uses this to select an omitted package revision. It is
			// metadata, so it must be validated and use the metadata TTL.
			kind = "metadata"
			ok = true
			return
		}
		if len(rest) == 11 && rest[6] == "packages" && rest[8] == "revisions" && rest[10] == "files" {
			path = strings.Join(rest, "/")
			kind = "metadata"
			ok = true
			return
		}
		if len(rest) == 12 && rest[6] == "packages" && rest[8] == "revisions" && rest[10] == "files" {
			path = strings.Join(rest, "/")
			kind = "file"
			file = rest[11]
			ok = true
			return
		}
	}
	return
}
func malformedConanPath(raw string) bool {
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 2 || parts[0] != "conan" {
		return false
	}
	for _, part := range parts[1:] {
		if !validConanSegment(part) {
			return true
		}
	}
	return false
}
func parseConanPing(method, path string) (string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return func() (string, bool) {
		if method == http.MethodGet && len(parts) == 4 && parts[0] == "conan" && parts[2] == "v1" && parts[3] == "ping" && validConanSegment(parts[1]) {
			return parts[1], true
		}
		return "", false
	}()
}
func parseConanAuthenticate(method, path string) (string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return func() (string, bool) {
		if method == http.MethodGet && len(parts) == 5 && parts[0] == "conan" && parts[2] == "v2" && parts[3] == "users" && parts[4] == "authenticate" && validConanSegment(parts[1]) {
			return parts[1], true
		}
		return "", false
	}()
}
func validConanSegment(s string) bool {
	decoded, err := url.PathUnescape(s)
	return err == nil && decoded != "" && decoded != "." && decoded != ".." && !strings.ContainsAny(decoded, "/\\\x00#") && !strings.Contains(strings.ToLower(s), "%2f") && !strings.Contains(strings.ToLower(s), "%23")
}
func validConanMetadata(path string, body []byte) bool {
	if strings.HasSuffix(path, "/latest") {
		var latest struct {
			Revision string `json:"revision"`
			Time     string `json:"time"`
		}
		if json.Unmarshal(body, &latest) != nil || !validConanSegment(latest.Revision) {
			return false
		}
		_, err := time.Parse(time.RFC3339, latest.Time)
		return err == nil
	}
	if strings.HasSuffix(path, "/search") {
		var search struct {
			Packages map[string]json.RawMessage `json:"packages"`
		}
		return json.Unmarshal(body, &search) == nil && search.Packages != nil
	}
	var data struct {
		Revisions json.RawMessage `json:"revisions"`
		Files     map[string]struct {
			SHA256 string `json:"sha256"`
			Size   int64  `json:"size"`
		} `json:"files"`
	}
	if json.Unmarshal(body, &data) != nil {
		return false
	}
	if strings.HasSuffix(path, "/files") {
		if data.Files == nil {
			return false
		}
		for name, entry := range data.Files {
			if !validConanSegment(name) || len(entry.SHA256) != 64 || entry.Size < 0 {
				return false
			}
			if _, err := hex.DecodeString(entry.SHA256); err != nil || entry.SHA256 != strings.ToLower(entry.SHA256) {
				return false
			}
		}
		return true
	}
	var revisions []struct {
		Revision string          `json:"revision"`
		Time     json.RawMessage `json:"time"`
	}
	if json.Unmarshal(data.Revisions, &revisions) != nil {
		return false
	}
	for _, revision := range revisions {
		if !validConanSegment(revision.Revision) || len(revision.Time) == 0 {
			return false
		}
		if revision.Time[0] == '"' {
			var timestamp string
			if json.Unmarshal(revision.Time, &timestamp) != nil || timestamp == "" {
				return false
			}
			if _, err := time.Parse(time.RFC3339, timestamp); err != nil {
				return false
			}
			continue
		}
		var numeric json.Number
		if json.Unmarshal(revision.Time, &numeric) != nil {
			return false
		}
		if _, err := numeric.Float64(); err != nil {
			return false
		}
	}
	return true
}
