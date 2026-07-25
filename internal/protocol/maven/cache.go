package maven

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/cache"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

var ErrCacheMiss = errors.New("maven cache miss")
var ErrNegativeCache = errors.New("maven negative cache hit")

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

type CachedContent struct {
	Body         []byte
	ContentType  string
	ETag         string
	LastModified string
	Member       string
	Endpoint     string
	Repository   string
}

// Cache stores complete upstream responses. Maven's metadata has a
// shorter lifetime than immutable-ish component files so version discovery
// cannot be pinned by a component-cache policy.
type Cache struct {
	store            objectstore.Store
	componentTTL     time.Duration
	metadataTTL      time.Duration
	negativeTTL      time.Duration
	breakerTTL       time.Duration
	allowedProxyHost map[string]struct{}
	mu               sync.Mutex
	openUntil        map[string]time.Time
	coordinator      cache.Coordinator
	quota            *cache.Quota
}

func NewCache(store objectstore.Store, componentTTL, metadataTTL, negativeTTL, breakerTTL time.Duration, allowedProxyHosts []string) *Cache {
	allowed := make(map[string]struct{}, len(allowedProxyHosts))
	for _, host := range allowedProxyHosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = struct{}{}
		}
	}
	return &Cache{store: store, componentTTL: componentTTL, metadataTTL: metadataTTL, negativeTTL: negativeTTL, breakerTTL: breakerTTL, allowedProxyHost: allowed, openUntil: make(map[string]time.Time)}
}

func NewDefaultCache(store objectstore.Store, allowedProxyHosts []string) *Cache {
	return NewCache(store, defaultMavenComponentCacheTTL, defaultMavenMetadataCacheTTL, defaultMavenNegativeCacheTTL, defaultMavenProxyBreakerTTL, allowedProxyHosts)
}

func (c *Cache) WithQuota(quota *cache.Quota) *Cache {
	c.quota = quota
	return c
}

func (c *Cache) WithCoordinator(coordinator cache.Coordinator) *Cache {
	c.coordinator = coordinator
	return c
}

func (c *Cache) Key(group, artifactPath string) string {
	sum := sha256.Sum256([]byte(group + "\x00" + artifactPath))
	return "maven/index/" + hex.EncodeToString(sum[:]) + ".json"
}

func (c *Cache) Load(ctx context.Context, key string) (CachedContent, error) {
	encoded, err := c.store.Get(ctx, key)
	if err != nil {
		return CachedContent{}, err
	}
	var index cachedMavenIndex
	if json.Unmarshal(encoded, &index) != nil || !time.Now().UTC().Before(index.ExpiresAt) {
		_ = c.store.Delete(ctx, key)
		return CachedContent{}, ErrCacheMiss
	}
	if index.Negative {
		return CachedContent{Member: index.Member, Endpoint: index.Endpoint}, ErrNegativeCache
	}
	body, err := c.store.Get(ctx, index.Object)
	if err != nil {
		_ = c.store.Delete(ctx, key)
		return CachedContent{}, ErrCacheMiss
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != index.Digest {
		_ = c.store.Delete(ctx, key)
		_ = c.store.Delete(ctx, index.Object)
		return CachedContent{}, ErrCacheMiss
	}
	return CachedContent{Body: body, ContentType: index.ContentType, ETag: index.ETag, LastModified: index.LastModified, Member: index.Member, Endpoint: index.Endpoint}, nil
}

func (c *Cache) Store(ctx context.Context, key, artifactPath string, content CachedContent) error {
	return c.quota.Admit(ctx, content.Repository, key, int64(len(content.Body)), func() error {
		return c.storeAdmitted(ctx, key, artifactPath, content)
	})
}

func (c *Cache) storeAdmitted(ctx context.Context, key, artifactPath string, content CachedContent) error {
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

func (c *Cache) StoreNegative(ctx context.Context, key string, member repository.Member) error {
	encoded, err := json.Marshal(cachedMavenIndex{Negative: true, Member: member.Name, Endpoint: member.Endpoint, ExpiresAt: time.Now().UTC().Add(c.negativeTTL)})
	if err != nil {
		return err
	}
	return c.store.Put(ctx, key, encoded)
}

func (c *Cache) Invalidate(ctx context.Context, key string) { _ = c.store.Delete(ctx, key) }

func (c *Cache) SetAllowedProxyHosts(hosts []string) {
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = struct{}{}
		}
	}
	c.mu.Lock()
	c.allowedProxyHost = allowed
	c.mu.Unlock()
}

func (c *Cache) ProxyAllowed(endpoint string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.allowedProxyHost) == 0 {
		return false
	}
	host := strings.ToLower(strings.Split(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/")[0])
	_, ok := c.allowedProxyHost[host]
	return ok
}

// WithRequestLock serializes an upstream cache miss across Gateway instances.
// Callers must re-read the cache inside work because the prior owner may have
// stored either a positive or negative entry.
func (c *Cache) WithRequestLock(ctx context.Context, key string, work func() error) (bool, error) {
	coordinator := c.coordinator
	if coordinator == nil {
		return false, nil
	}
	for {
		owner, acquired, err := coordinator.Acquire(ctx, "cache-request:"+key, cache.DefaultLockLease)
		if err != nil {
			return false, err
		}
		if acquired {
			defer func() {
				releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_ = coordinator.Release(releaseCtx, "cache-request:"+key, owner)
			}()
			return true, work()
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (c *Cache) UpstreamAllowed(ctx context.Context, endpoint string) bool {
	if c.coordinator != nil {
		open, err := c.coordinator.CircuitOpen(ctx, endpoint)
		if err == nil && open {
			return false
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	until := c.openUntil[endpoint]
	return until.IsZero() || !time.Now().UTC().Before(until)
}

func (c *Cache) RecordUpstreamFailure(ctx context.Context, endpoint string) {
	if c.coordinator != nil {
		_ = c.coordinator.OpenCircuit(ctx, endpoint, c.breakerTTL)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openUntil[endpoint] = time.Now().UTC().Add(c.breakerTTL)
}

func (c *Cache) RecordUpstreamSuccess(ctx context.Context, endpoint string) {
	if c.coordinator != nil {
		_ = c.coordinator.CloseCircuit(ctx, endpoint)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.openUntil, endpoint)
}

func isMavenMetadata(path string) bool {
	return strings.HasSuffix(path, "/maven-metadata.xml") || path == "maven-metadata.xml"
}
