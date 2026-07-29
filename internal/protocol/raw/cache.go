package raw

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	cache "github.com/artifact-gateway/artifact-gateway/internal/cache"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

var ErrCacheMiss = errors.New("raw cache miss")
var ErrNegativeCache = errors.New("raw negative cache hit")

const defaultCacheTTL = 15 * time.Minute
const defaultNegativeCacheTTL = time.Minute
const defaultMaxObjectBytes = int64(1 << 30)
const distributedLockLease = 35 * time.Second

type cacheIndex struct {
	Object      string    `json:"object"`
	Digest      string    `json:"digest"`
	ContentType string    `json:"content_type"`
	Member      string    `json:"member"`
	Endpoint    string    `json:"endpoint"`
	Repository  string    `json:"repository"`
	Path        string    `json:"path,omitempty"`
	Size        int64     `json:"size"`
	ExpiresAt   time.Time `json:"expires_at"`
	Negative    bool      `json:"negative"`
}

func (i *cacheIndex) UnmarshalJSON(data []byte) error {
	type encodedCacheIndex cacheIndex
	var decoded encodedCacheIndex
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.ExpiresAt.IsZero() {
		var legacy struct {
			Object, Digest, ContentType, Member, Endpoint, Repository, Path string
			Size                                                            int64
			ExpiresAt                                                       time.Time
			Negative                                                        bool
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return err
		}
		decoded = encodedCacheIndex(legacy)
	}
	*i = cacheIndex(decoded)
	return nil
}

type CachedContent struct {
	Body                                              []byte
	Digest, ContentType, Member, Endpoint, Repository string
	Path                                              string
	CacheQuotaBytes                                   int64
}

// Cache owns Raw cache publication ordering, negative entries, index
// compatibility, garbage collection, and proxy host admission.
type Cache struct {
	store            objectstore.Store
	ttl, negativeTTL time.Duration
	maxObjectBytes   int64
	allowed          map[string]struct{}
	quota            *cache.Quota
	mu               sync.Mutex
	coordinator      cache.Coordinator
	lockRenewEvery   time.Duration
	publicationMu    sync.Mutex
}

func NewCache(store objectstore.Store, ttl, negativeTTL time.Duration, hosts []string) *Cache {
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = struct{}{}
		}
	}
	return &Cache{store: store, ttl: ttl, negativeTTL: negativeTTL, maxObjectBytes: defaultMaxObjectBytes, allowed: allowed, lockRenewEvery: distributedLockLease / 3}
}

func NewDefaultCache(store objectstore.Store, hosts []string) *Cache {
	return NewCache(store, defaultCacheTTL, defaultNegativeCacheTTL, hosts)
}

func (c *Cache) WithQuota(quota *cache.Quota) *Cache { c.quota = quota; return c }
func (c *Cache) WithCoordinator(coordinator cache.Coordinator) *Cache {
	c.coordinator = coordinator
	return c
}
func (c *Cache) WithMaxObjectBytes(limit int64) *Cache {
	if limit > 0 {
		c.maxObjectBytes = limit
	}
	return c
}

// WithTTL overrides the positive cache TTL; negative entries keep their
// shorter lifetime.
func (c *Cache) WithTTL(ttl time.Duration) *Cache {
	if ttl > 0 {
		c.ttl = ttl
	}
	return c
}

func (c *Cache) MaxObjectBytes() int64 { return c.maxObjectBytes }

func (c *Cache) Key(group, path, member, endpoint string) string {
	sum := sha256.Sum256([]byte(group + "\x00" + path + "\x00" + member + "\x00" + endpoint))
	return "raw/index/" + hex.EncodeToString(sum[:]) + ".json"
}

func (c *Cache) Load(ctx context.Context, key string) (CachedContent, error) {
	encoded, err := c.store.Get(ctx, key)
	if err != nil {
		return CachedContent{}, err
	}
	var index cacheIndex
	if json.Unmarshal(encoded, &index) != nil || !time.Now().UTC().Before(index.ExpiresAt) {
		c.removeIndex(ctx, key, encoded)
		return CachedContent{}, ErrCacheMiss
	}
	if index.Negative {
		return CachedContent{Member: index.Member, Endpoint: index.Endpoint}, ErrNegativeCache
	}
	body, err := c.store.Get(ctx, index.Object)
	if err != nil {
		c.removeIndex(ctx, key, encoded)
		return CachedContent{}, ErrCacheMiss
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != index.Digest {
		c.removeIndex(ctx, key, encoded)
		return CachedContent{}, ErrCacheMiss
	}
	return CachedContent{Body: body, Digest: index.Digest, ContentType: index.ContentType, Member: index.Member, Endpoint: index.Endpoint, Repository: index.Repository}, nil
}

func (c *Cache) Store(ctx context.Context, key string, content CachedContent) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.quota.AdmitWithLimit(workCtx, content.Repository, key, int64(len(content.Body)), content.CacheQuotaBytes, func() error {
			sum := sha256.Sum256(content.Body)
			digest := hex.EncodeToString(sum[:])
			object := "raw/objects/" + digest
			if err := c.store.Put(workCtx, object, content.Body); err != nil {
				return err
			}
			encoded, err := json.Marshal(cacheIndex{Object: object, Digest: digest, ContentType: content.ContentType, Member: content.Member, Endpoint: content.Endpoint, Repository: content.Repository, Path: content.Path, Size: int64(len(content.Body)), ExpiresAt: time.Now().UTC().Add(c.ttl)})
			if err != nil {
				return err
			}
			return c.store.Put(workCtx, key, encoded)
		})
	})
}

func (c *Cache) StoreNegative(ctx context.Context, key string, member repository.Member) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		encoded, err := json.Marshal(cacheIndex{Member: member.Name, Endpoint: member.Endpoint, Negative: true, ExpiresAt: time.Now().UTC().Add(c.negativeTTL)})
		if err != nil {
			return err
		}
		return c.store.Put(workCtx, key, encoded)
	})
}

func (c *Cache) Invalidate(ctx context.Context, key string) { c.removeIndex(ctx, key, nil) }

func (c *Cache) CollectGarbage(ctx context.Context) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		keys, err := c.store.List(workCtx, "raw/index/")
		if err != nil {
			return err
		}
		referenced := map[string]bool{}
		now := time.Now().UTC()
		for _, key := range keys {
			encoded, err := c.store.Get(workCtx, key)
			if err != nil {
				continue
			}
			var index cacheIndex
			if json.Unmarshal(encoded, &index) != nil || !now.Before(index.ExpiresAt) {
				_ = c.store.Delete(workCtx, key)
				continue
			}
			if index.Object != "" {
				referenced[index.Object] = true
			}
		}
		objects, err := c.store.List(workCtx, "raw/objects/")
		if err != nil {
			return err
		}
		for _, object := range objects {
			if !referenced[object] {
				_ = c.store.Delete(workCtx, object)
			}
		}
		return nil
	})
}

func (c *Cache) AcquireRequestLock(ctx context.Context, key string) (func(), error) {
	if c.coordinator == nil {
		return func() {}, nil
	}
	for {
		owner, acquired, err := c.coordinator.Acquire(ctx, "cache-request:"+key, distributedLockLease)
		if err != nil {
			return nil, err
		}
		if acquired {
			return func() {
				releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_ = c.coordinator.Release(releaseCtx, "cache-request:"+key, owner)
			}, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (c *Cache) ProxyAllowed(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && privateIP(ip) {
		return false
	}
	_, ok := c.allowed[strings.ToLower(u.Hostname())]
	return ok
}

func (c *Cache) removeIndex(ctx context.Context, key string, expected []byte) {
	_ = c.withPublicationLock(ctx, func(workCtx context.Context) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if expected != nil {
			current, err := c.store.Get(workCtx, key)
			if err != nil || !bytes.Equal(current, expected) {
				return err
			}
		}
		return c.store.Delete(workCtx, key)
	})
}

func (c *Cache) withPublicationLock(ctx context.Context, work func(context.Context) error) error {
	c.publicationMu.Lock()
	defer c.publicationMu.Unlock()
	if c.coordinator == nil {
		return work(ctx)
	}
	for {
		owner, acquired, err := c.coordinator.Acquire(ctx, "raw-publication", distributedLockLease)
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
			_ = c.coordinator.Release(releaseCtx, "raw-publication", owner)
		}()
		workCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		renewalFailed := make(chan struct{})
		go c.renewPublicationLock(workCtx, owner, renewalFailed, cancel)
		err = work(workCtx)
		select {
		case <-renewalFailed:
			return errors.New("raw distributed publication lock renewal failed")
		default:
			return err
		}
	}
}

func (c *Cache) renewPublicationLock(ctx context.Context, owner string, failed chan<- struct{}, cancel context.CancelFunc) {
	ticker := time.NewTicker(c.lockRenewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := c.coordinator.Renew(ctx, "raw-publication", owner, distributedLockLease)
			if err != nil || !ok {
				close(failed)
				cancel()
				return
			}
		}
	}
}

func privateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()
}
