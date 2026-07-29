package app

import (
	"context"
	"time"

	conanprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/conan"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// ConanCache keeps the existing handler composition surface while cache
// ownership lives in the Conan protocol module.
type ConanCache struct{ *conanprotocol.Cache }

// conanCacheIndex remains a read model for existing app-level cache tests.
type conanCacheIndex struct{ ExpiresAt time.Time }

func NewConanCache(_ []string) *ConanCache {
	return NewConanCacheWithStore(NewMemoryOCIObjectStore(), nil)
}
func NewConanCacheWithStore(store OCIObjectStore, _ []string) *ConanCache {
	return &ConanCache{Cache: conanprotocol.NewCache(store)}
}
func NewDefaultConanCache(store OCIObjectStore, hosts []string) *ConanCache {
	return &ConanCache{Cache: conanprotocol.NewDefaultCache(store, hosts)}
}
func (c *ConanCache) WithQuota(quota *CacheQuota) *ConanCache { c.Cache.WithQuota(quota); return c }
func (c *ConanCache) WithCoordinator(coordinator OCICacheCoordinator) *ConanCache {
	c.Cache.WithCoordinator(coordinator)
	return c
}
func (c *ConanCache) WithMaxObjectBytes(limit int64) *ConanCache {
	c.Cache.WithMaxObjectBytes(limit)
	return c
}
func (c *ConanCache) WithTTL(ttl time.Duration) *ConanCache {
	c.Cache.WithTTL(ttl)
	return c
}
func (c *ConanCache) load(ctx context.Context, key string) (conanCacheEntry, bool) {
	content, ok := c.Load(ctx, key)
	return conanCacheEntry{body: content.Body, contentType: content.ContentType, member: content.Member, endpoint: content.Endpoint, status: content.Status}, ok
}
func (c *ConanCache) store(ctx context.Context, key string, entry conanCacheEntry, repositoryName string, quotaBytes int64, ttl time.Duration, identity ...string) error {
	return c.Store(ctx, key, conanprotocol.Content{Body: entry.body, ContentType: entry.contentType, Member: entry.member, Endpoint: entry.endpoint, Status: entry.status}, repositoryName, quotaBytes, ttl, identity...)
}
func (c *ConanCache) key(group, path string, member repository.Member, representation ...string) string {
	return c.Key(group, path, member, representation...)
}
func (c *ConanCache) proxyAllowed(member repository.Member) bool { return c.ProxyAllowed(member) }
