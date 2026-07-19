package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

var errMavenCacheMiss = errors.New("maven cache miss")
var errMavenCacheNegative = errors.New("maven negative cache hit")

const defaultMavenComponentCacheTTL = 15 * time.Minute
const defaultMavenMetadataCacheTTL = time.Minute
const defaultMavenNegativeCacheTTL = time.Minute
const defaultMavenProxyBreakerTTL = 30 * time.Second

type cachedMavenIndex struct {
	Object       string    `json:"object,omitempty"`
	Digest       string    `json:"digest,omitempty"`
	Repository   string    `json:"repository,omitempty"`
	Size         int64     `json:"size,omitempty"`
	ContentType  string    `json:"content_type,omitempty"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	Member       string    `json:"member,omitempty"`
	Endpoint     string    `json:"endpoint,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	Negative     bool      `json:"negative,omitempty"`
}

type CachedMavenContent struct {
	Body         []byte
	ContentType  string
	ETag         string
	LastModified string
	Member       string
	Endpoint     string
	Repository   string
}

// MavenCache stores complete upstream responses. Maven's metadata has a
// shorter lifetime than immutable-ish component files so version discovery
// cannot be pinned by a component-cache policy.
type MavenCache struct {
	store            OCIObjectStore
	componentTTL     time.Duration
	metadataTTL      time.Duration
	negativeTTL      time.Duration
	breakerTTL       time.Duration
	allowedProxyHost map[string]struct{}
	mu               sync.Mutex
	openUntil        map[string]time.Time
	quota            *CacheQuota
}

func NewMavenCache(store OCIObjectStore, componentTTL, metadataTTL, negativeTTL, breakerTTL time.Duration, allowedProxyHosts []string) *MavenCache {
	allowed := make(map[string]struct{}, len(allowedProxyHosts))
	for _, host := range allowedProxyHosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = struct{}{}
		}
	}
	return &MavenCache{store: store, componentTTL: componentTTL, metadataTTL: metadataTTL, negativeTTL: negativeTTL, breakerTTL: breakerTTL, allowedProxyHost: allowed, openUntil: make(map[string]time.Time)}
}

func NewDefaultMavenCache(store OCIObjectStore, allowedProxyHosts []string) *MavenCache {
	return NewMavenCache(store, defaultMavenComponentCacheTTL, defaultMavenMetadataCacheTTL, defaultMavenNegativeCacheTTL, defaultMavenProxyBreakerTTL, allowedProxyHosts)
}

func (c *MavenCache) WithQuota(quota *CacheQuota) *MavenCache {
	c.quota = quota
	return c
}

func (c *MavenCache) key(group, artifactPath string) string {
	sum := sha256.Sum256([]byte(group + "\x00" + artifactPath))
	return "maven/index/" + hex.EncodeToString(sum[:]) + ".json"
}

func (c *MavenCache) Load(ctx context.Context, key string) (CachedMavenContent, error) {
	encoded, err := c.store.Get(ctx, key)
	if err != nil {
		return CachedMavenContent{}, err
	}
	var index cachedMavenIndex
	if json.Unmarshal(encoded, &index) != nil || !time.Now().UTC().Before(index.ExpiresAt) {
		_ = c.store.Delete(ctx, key)
		return CachedMavenContent{}, errMavenCacheMiss
	}
	if index.Negative {
		return CachedMavenContent{Member: index.Member, Endpoint: index.Endpoint}, errMavenCacheNegative
	}
	body, err := c.store.Get(ctx, index.Object)
	if err != nil {
		_ = c.store.Delete(ctx, key)
		return CachedMavenContent{}, errMavenCacheMiss
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != index.Digest {
		_ = c.store.Delete(ctx, key)
		_ = c.store.Delete(ctx, index.Object)
		return CachedMavenContent{}, errMavenCacheMiss
	}
	return CachedMavenContent{Body: body, ContentType: index.ContentType, ETag: index.ETag, LastModified: index.LastModified, Member: index.Member, Endpoint: index.Endpoint}, nil
}

func (c *MavenCache) Store(ctx context.Context, key, artifactPath string, content CachedMavenContent) error {
	return c.quota.Admit(ctx, content.Repository, key, int64(len(content.Body)), func() error {
		return c.storeAdmitted(ctx, key, artifactPath, content)
	})
}

func (c *MavenCache) storeAdmitted(ctx context.Context, key, artifactPath string, content CachedMavenContent) error {
	sum := sha256.Sum256(content.Body)
	digest := hex.EncodeToString(sum[:])
	object := "maven/objects/" + digest
	if err := c.store.Put(ctx, object, content.Body); err != nil {
		return err
	}
	ttl := c.componentTTL
	if isMavenMetadata(artifactPath) {
		ttl = c.metadataTTL
	}
	encoded, err := json.Marshal(cachedMavenIndex{Object: object, Digest: digest, Repository: content.Repository, Size: int64(len(content.Body)), ContentType: content.ContentType, ETag: content.ETag, LastModified: content.LastModified, Member: content.Member, Endpoint: content.Endpoint, ExpiresAt: time.Now().UTC().Add(ttl)})
	if err != nil {
		return err
	}
	return c.store.Put(ctx, key, encoded)
}

func (c *MavenCache) StoreNegative(ctx context.Context, key string, member repository.Member) error {
	encoded, err := json.Marshal(cachedMavenIndex{Negative: true, Member: member.Name, Endpoint: member.Endpoint, ExpiresAt: time.Now().UTC().Add(c.negativeTTL)})
	if err != nil {
		return err
	}
	return c.store.Put(ctx, key, encoded)
}

func (c *MavenCache) Invalidate(ctx context.Context, key string) { _ = c.store.Delete(ctx, key) }

func (c *MavenCache) ProxyAllowed(endpoint string) bool {
	if len(c.allowedProxyHost) == 0 {
		return false
	}
	host := strings.ToLower(strings.Split(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/")[0])
	_, ok := c.allowedProxyHost[host]
	return ok
}

func (c *MavenCache) UpstreamAllowed(endpoint string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	until := c.openUntil[endpoint]
	return until.IsZero() || !time.Now().UTC().Before(until)
}

func (c *MavenCache) RecordUpstreamFailure(endpoint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openUntil[endpoint] = time.Now().UTC().Add(c.breakerTTL)
}

func (c *MavenCache) RecordUpstreamSuccess(endpoint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.openUntil, endpoint)
}

func isMavenMetadata(path string) bool {
	return strings.HasSuffix(path, "/maven-metadata.xml") || path == "maven-metadata.xml"
}
