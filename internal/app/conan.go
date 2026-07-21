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
		if err := safeRawProxyEndpoint(ctx, member.Endpoint); err != nil {
			return nil, err
		}
	}
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
	client := tracedHTTPClient(c.HTTPClient)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return client.Do(r)
}

type conanCacheEntry struct {
	body                          []byte
	contentType, member, endpoint string
	status                        int
	expires                       time.Time
}
type ConanCache struct {
	mu      sync.Mutex
	entries map[string]conanCacheEntry
	allowed map[string]struct{}
}

func NewConanCache(hosts []string) *ConanCache {
	c := &ConanCache{entries: map[string]conanCacheEntry{}, allowed: map[string]struct{}{}}
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			c.allowed[h] = struct{}{}
		}
	}
	return c
}
func (c *ConanCache) load(key string) (conanCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || !time.Now().UTC().Before(e.expires) {
		delete(c.entries, key)
		return conanCacheEntry{}, false
	}
	return e, true
}
func (c *ConanCache) store(key string, e conanCacheEntry, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.expires = time.Now().UTC().Add(ttl)
	c.entries[key] = e
}
func (c *ConanCache) key(group, path string, member repository.Member) string {
	return group + "\x00" + path + "\x00" + member.Name + "\x00" + member.Endpoint
}
func (c *ConanCache) proxyAllowed(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && privateAddress(ip) {
		return false
	}
	_, allowed := c.allowed[strings.ToLower(u.Hostname())]
	return allowed
}

type ConanHandler struct {
	Store         repository.ConanStore
	Authenticator Authenticator
	Client        ConanClient
	Cache         *ConanCache
	Metrics       *Metrics
}

func (h ConanHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if group, ok := parseConanPing(r.Method, r.URL.Path); ok {
		if h.anonymousConanAllowed(r.Context(), group) {
			w.Header().Set("X-Conan-Server-Capabilities", "revisions")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	group, path, kind, file, ok := parseConanPath(r.Method, r.URL.EscapedPath())
	if !ok {
		http.NotFound(w, r)
		return
	}
	p, authenticated := h.Authenticator.Authenticate(r.Header.Get("Authorization"))
	if !authenticated && h.anonymousConanAllowed(r.Context(), group) {
		p = Principal{Actor: "anonymous"}
		authenticated = true
	}
	if !authenticated {
		w.Header().Set("WWW-Authenticate", `Basic realm="Artifact Gateway Conan"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if kind == "package_search" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
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
	content, status, err := h.resolve(r.Context(), g, path, kind, r.Header, p.Actor)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	if file != "" && !h.verifyFile(r.Context(), g, path, file, content, p.Actor) {
		http.Error(w, "Conan file checksum mismatch", http.StatusBadGateway)
		return
	}
	if file != "" && h.Cache != nil {
		h.Cache.store(h.Cache.key(group, path, repository.Member{Name: content.member, Endpoint: content.endpoint}), content, 15*time.Minute)
	}
	w.Header().Set("Content-Type", content.contentType)
	w.Header().Set("Content-Length", fmt.Sprint(len(content.body)))
	if r.Method == http.MethodGet {
		_, _ = w.Write(content.body)
	}
}

func (h ConanHandler) resolve(ctx context.Context, group repository.Group, path, kind string, headers http.Header, actor string) (conanCacheEntry, int, error) {
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
		key := ""
		if h.Cache != nil {
			key = h.Cache.key(group.Name, path, member)
			if e, ok := h.Cache.load(key); ok {
				if e.status == http.StatusNotFound {
					h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditNotFound)
					continue
				}
				h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditResolved)
				return e, http.StatusOK, nil
			}
		}
		if member.Type == repository.MemberProxy && (h.Cache == nil || !h.Cache.proxyAllowed(member.Endpoint)) {
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
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode >= 500 || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests {
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			hadFailure = true
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			if h.Cache != nil {
				h.Cache.store(key, conanCacheEntry{status: http.StatusNotFound, member: member.Name, endpoint: member.Endpoint}, time.Minute)
			}
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditNotFound)
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			hadFailure = true
			continue
		}
		if metadata && !validConanMetadata(path, body) {
			h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditUpstreamError)
			return conanCacheEntry{}, http.StatusBadGateway, errors.New("invalid Conan metadata")
		}
		e := conanCacheEntry{body: body, contentType: response.Header.Get("Content-Type"), member: member.Name, endpoint: member.Endpoint, status: response.StatusCode}
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
			h.Cache.store(key, e, ttl)
		}
		h.audit(ctx, group.Name, path, member.Name, actor, repository.AuditResolved)
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

func (h ConanHandler) verifyFile(ctx context.Context, group repository.Group, path, file string, content conanCacheEntry, actor string) bool {
	metadataPath := path[:strings.LastIndex(path, "/files/")] + "/files"
	metadata, status, err := h.resolve(ctx, group, metadataPath, "metadata", http.Header{}, actor)
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
func (h ConanHandler) audit(ctx context.Context, group, path, member, actor string, outcome repository.AuditOutcome) {
	_ = h.Store.RecordAudit(ctx, repository.AuditRecord{GroupName: group, Repository: path, MemberName: member, Actor: actor, Outcome: outcome})
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
	if len(rest) >= 7 && rest[4] == "revisions" {
		if len(rest) == 7 && rest[6] == "search" {
			path = strings.Join(rest, "/")
			kind = "package_search"
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
func parseConanPing(method, path string) (string, bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return func() (string, bool) {
		if method == http.MethodGet && len(parts) == 4 && parts[0] == "conan" && parts[2] == "v1" && parts[3] == "ping" && validConanSegment(parts[1]) {
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
		var numeric json.Number
		if json.Unmarshal(revision.Time, &numeric) != nil {
			var timestamp string
			if json.Unmarshal(revision.Time, &timestamp) != nil || timestamp == "" {
				return false
			}
		}
	}
	return true
}
