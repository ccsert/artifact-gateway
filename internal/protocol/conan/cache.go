package conan

import (
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

	"github.com/artifact-gateway/artifact-gateway/internal/cache"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

const defaultMaxObjectBytes = int64(1 << 30)

type Content struct {
	Body                          []byte
	ContentType, Member, Endpoint string
	Status                        int
}
type cacheIndex struct {
	Object, Digest, ContentType, Member, Endpoint, Repository, Group, Path, Representation string
	Size                                                                                   int64
	Status                                                                                 int
	ExpiresAt                                                                              time.Time
	Negative                                                                               bool
}

// Cache owns the Conan cache index, its object lifetime, and cross-instance coordination.
type Cache struct {
	store          objectstore.Store
	quota          *cache.Quota
	maxObjectBytes int64
	coordinator    cache.Coordinator
	publicationMu  sync.Mutex
}

func NewCache(store objectstore.Store) *Cache {
	return &Cache{store: store, maxObjectBytes: defaultMaxObjectBytes}
}
func NewDefaultCache(store objectstore.Store, _ []string) *Cache { return NewCache(store) }
func (c *Cache) WithQuota(q *cache.Quota) *Cache                 { c.quota = q; return c }
func (c *Cache) WithCoordinator(v cache.Coordinator) *Cache      { c.coordinator = v; return c }
func (c *Cache) WithMaxObjectBytes(limit int64) *Cache {
	if limit > 0 {
		c.maxObjectBytes = limit
	}
	return c
}
func (c *Cache) MaxObjectBytes() int64 { return c.maxObjectBytes }

func (c *Cache) Key(group, path string, member repository.Member, representation ...string) string {
	v := ""
	if len(representation) > 0 {
		v = representation[0]
	}
	sum := sha256.Sum256([]byte(group + "\x00" + path + "\x00" + member.Name + "\x00" + member.Endpoint + "\x00" + v))
	return "conan/index/" + hex.EncodeToString(sum[:]) + ".json"
}
func (c *Cache) Load(ctx context.Context, key string) (Content, bool) {
	encoded, err := c.store.Get(ctx, key)
	if err != nil {
		return Content{}, false
	}
	var index cacheIndex
	if json.Unmarshal(encoded, &index) != nil || !time.Now().UTC().Before(index.ExpiresAt) {
		_ = c.store.Delete(ctx, key)
		return Content{}, false
	}
	if index.Negative {
		return Content{Status: 404, Member: index.Member, Endpoint: index.Endpoint}, true
	}
	body, err := c.store.Get(ctx, index.Object)
	if err != nil {
		_ = c.store.Delete(ctx, key)
		return Content{}, false
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != index.Digest {
		_ = c.store.Delete(ctx, key)
		_ = c.store.Delete(ctx, index.Object)
		return Content{}, false
	}
	return Content{Body: body, ContentType: index.ContentType, Member: index.Member, Endpoint: index.Endpoint, Status: index.Status}, true
}
func (c *Cache) Store(ctx context.Context, key string, content Content, repositoryName string, quotaBytes int64, ttl time.Duration, identity ...string) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		return c.quota.AdmitConanWithLimit(workCtx, repositoryName, key, int64(len(content.Body)), quotaBytes, func() error {
			sum := sha256.Sum256(content.Body)
			digest := hex.EncodeToString(sum[:])
			object := "conan/objects/" + digest
			if content.Status != 404 {
				if err := c.store.Put(workCtx, object, content.Body); err != nil {
					return err
				}
			}
			index := cacheIndex{Object: object, Digest: digest, ContentType: content.ContentType, Member: content.Member, Endpoint: content.Endpoint, Repository: repositoryName, Size: int64(len(content.Body)), Status: content.Status, ExpiresAt: time.Now().UTC().Add(ttl), Negative: content.Status == 404}
			if len(identity) == 3 {
				index.Group, index.Path, index.Representation = identity[0], identity[1], identity[2]
			}
			encoded, err := json.Marshal(index)
			if err != nil {
				return err
			}
			return c.store.Put(workCtx, key, encoded)
		})
	})
}
func (c *Cache) Invalidate(ctx context.Context, group, path string, member repository.Member) {
	_ = c.withPublicationLock(ctx, func(workCtx context.Context) error {
		_ = c.store.Delete(workCtx, c.Key(group, path, member))
		keys, err := c.store.List(workCtx, "conan/index/")
		if err != nil {
			return err
		}
		for _, key := range keys {
			encoded, err := c.store.Get(workCtx, key)
			if err != nil {
				continue
			}
			var index cacheIndex
			if json.Unmarshal(encoded, &index) == nil && index.Group == group && index.Path == path && index.Member == member.Name && index.Endpoint == member.Endpoint {
				_ = c.store.Delete(workCtx, key)
			}
		}
		return nil
	})
}
func (c *Cache) CollectGarbage(ctx context.Context) error {
	return c.withPublicationLock(ctx, c.collectGarbage)
}
func (c *Cache) collectGarbage(ctx context.Context) error {
	keys, err := c.store.List(ctx, "conan/index/")
	if err != nil {
		return err
	}
	referenced := map[string]bool{}
	for _, key := range keys {
		encoded, err := c.store.Get(ctx, key)
		if err != nil {
			continue
		}
		var index cacheIndex
		if json.Unmarshal(encoded, &index) != nil || !time.Now().UTC().Before(index.ExpiresAt) {
			_ = c.store.Delete(ctx, key)
			continue
		}
		if index.Object != "" {
			referenced[index.Object] = true
		}
	}
	objects, err := c.store.List(ctx, "conan/objects/")
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
func (c *Cache) ProxyAllowed(member repository.Member) bool {
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
func privateAddress(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()
}

// AcquireRequestLock serializes metadata misses across Gateway instances.
func (c *Cache) AcquireRequestLock(ctx context.Context, key string) (func(), error) {
	if c.coordinator == nil {
		return func() {}, nil
	}
	for {
		owner, ok, err := c.coordinator.Acquire(ctx, "cache-request:"+key, cache.DefaultLockLease)
		if err != nil {
			return nil, err
		}
		if ok {
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
func (c *Cache) withPublicationLock(ctx context.Context, work func(context.Context) error) error {
	c.publicationMu.Lock()
	defer c.publicationMu.Unlock()
	if c.coordinator == nil {
		return work(ctx)
	}
	for {
		owner, ok, err := c.coordinator.Acquire(ctx, "conan-publication", cache.DefaultLockLease)
		if err != nil {
			return err
		}
		if !ok {
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
		go c.renewPublicationLock(workCtx, owner, failed, cancel)
		err = work(workCtx)
		select {
		case <-failed:
			return ErrLockRenewal
		default:
			return err
		}
	}
}

func (c *Cache) renewPublicationLock(ctx context.Context, owner string, failed chan<- struct{}, cancel context.CancelFunc) {
	ticker := time.NewTicker(cache.DefaultLockRenewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := c.coordinator.Renew(ctx, "conan-publication", owner, cache.DefaultLockLease)
			if err != nil || !ok {
				close(failed)
				cancel()
				return
			}
		}
	}
}

var ErrLockRenewal = errors.New("conan distributed publication lock renewal failed")
