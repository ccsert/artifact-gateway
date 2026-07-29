package oci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/cache"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"golang.org/x/sync/singleflight"
)

var ErrCacheMiss = objectstore.ErrNotFound
var ErrNegativeCache = errors.New("OCI negative cache hit")

const defaultOCICacheTTL = 15 * time.Minute
const defaultOCINegativeCacheTTL = time.Minute
const defaultOCIProxyBreakerTTL = 30 * time.Second
const ociDistributedLockLease = 35 * time.Second
const ociDistributedLockRenewInterval = ociDistributedLockLease / 3

type cachedOCIIndex struct {
	Object      string    `json:"object,omitempty"`
	Digest      string    `json:"digest,omitempty"`
	Repository  string    `json:"repository,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Member      string    `json:"member,omitempty"`
	Endpoint    string    `json:"endpoint,omitempty"`
	Size        int64     `json:"size,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
	Negative    bool      `json:"negative,omitempty"`
}

type ociGarbageCandidate struct {
	Object      string    `json:"object"`
	DeleteAfter time.Time `json:"delete_after"`
}

type CachedContent struct {
	Body        []byte
	Digest      string
	ContentType string
	Member      string
	Endpoint    string
	Repository  string
	Object      string
	Size        int64
	tempPath    string
	store       objectstore.Store
	cacheable   bool
}

func NewVerifiedContent(digest string, size int64, tempPath string) CachedContent {
	return CachedContent{Digest: digest, Size: size, tempPath: tempPath}
}

func NewStoredContent(digest, contentType, object string, size int64, store objectstore.Store) CachedContent {
	return CachedContent{Digest: digest, ContentType: contentType, Object: object, Size: size, store: store}
}

func (c CachedContent) Cacheable() bool { return c.cacheable }

func (c *CachedContent) SetCacheable(cacheable bool) { c.cacheable = cacheable }

func (c CachedContent) HasTemporaryReader() bool { return c.tempPath != "" }

func (c CachedContent) Cleanup() {
	if c.tempPath != "" {
		_ = os.Remove(c.tempPath)
	}
}

func (c CachedContent) Open(ctx context.Context) (io.ReadCloser, int64, error) {
	if c.tempPath != "" {
		file, err := os.Open(c.tempPath)
		if err != nil {
			return nil, 0, err
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, 0, err
		}
		return file, info.Size(), nil
	}
	if c.Object != "" {
		if c.store == nil {
			return nil, 0, errors.New("cached object has no object store")
		}
		return c.store.Open(ctx, c.Object)
	}
	return io.NopCloser(bytes.NewReader(c.Body)), int64(len(c.Body)), nil
}

func (c CachedContent) OpenRange(ctx context.Context, offset, length int64) (io.ReadCloser, int64, error) {
	if c.tempPath != "" {
		file, err := os.Open(c.tempPath)
		if err != nil {
			return nil, 0, err
		}
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			_ = file.Close()
			return nil, 0, err
		}
		return struct {
			io.Reader
			io.Closer
		}{Reader: io.LimitReader(file, length), Closer: file}, c.Size, nil
	}
	if c.Object != "" && c.store != nil {
		return c.store.OpenRange(ctx, c.Object, offset, length)
	}
	if offset < 0 || length < 0 || offset > int64(len(c.Body)) || length > int64(len(c.Body))-offset {
		return nil, 0, errors.New("OCI content range is out of bounds")
	}
	return io.NopCloser(bytes.NewReader(c.Body[offset : offset+length])), int64(len(c.Body)), nil
}

type Cache struct {
	store            objectstore.Store
	ttl              time.Duration
	negativeTTL      time.Duration
	breakerTTL       time.Duration
	allowedProxyHost map[string]struct{}
	group            singleflight.Group
	mu               sync.Mutex
	openUntil        map[string]time.Time
	coordinator      cache.Coordinator
	quota            *cache.Quota
	lockLease        time.Duration
	lockRenewEvery   time.Duration
	gcGrace          time.Duration
	publicationMu    sync.Mutex
}

func NewCache(store objectstore.Store, ttl, negativeTTL, breakerTTL time.Duration, allowedProxyHosts []string) *Cache {
	allowed := make(map[string]struct{}, len(allowedProxyHosts))
	for _, host := range allowedProxyHosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = struct{}{}
		}
	}
	return &Cache{store: store, ttl: ttl, negativeTTL: negativeTTL, breakerTTL: breakerTTL, allowedProxyHost: allowed, openUntil: make(map[string]time.Time), lockLease: ociDistributedLockLease, lockRenewEvery: ociDistributedLockRenewInterval, gcGrace: ttl}
}

func NewDefaultCache(store objectstore.Store, allowedProxyHosts []string) *Cache {
	return NewCache(store, defaultOCICacheTTL, defaultOCINegativeCacheTTL, defaultOCIProxyBreakerTTL, allowedProxyHosts)
}

func (c *Cache) WithCoordinator(coordinator cache.Coordinator) *Cache {
	c.coordinator = coordinator
	return c
}

func (c *Cache) WithQuota(quota *cache.Quota) *Cache {
	c.quota = quota
	return c
}

// WithTTL overrides the positive cache TTL. The garbage-collection grace
// tracks the TTL so objects outlive the index that references them.
func (c *Cache) WithTTL(ttl time.Duration) *Cache {
	if ttl > 0 {
		c.ttl = ttl
		c.gcGrace = ttl
	}
	return c
}

func (c *Cache) WithLockTiming(lease, renewEvery time.Duration) *Cache {
	if lease > 0 {
		c.lockLease = lease
	}
	if renewEvery > 0 {
		c.lockRenewEvery = renewEvery
	}
	return c
}

func (c *Cache) WithGarbageCollectionGrace(grace time.Duration) *Cache {
	if grace >= 0 {
		c.gcGrace = grace
	}
	return c
}

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

func (c *Cache) Key(group, repository, resource, reference string) string {
	sum := sha256.Sum256([]byte(group + "\x00" + repository + "\x00" + resource + "\x00" + reference))
	return "oci/index/" + hex.EncodeToString(sum[:]) + ".json"
}

func (c *Cache) Load(ctx context.Context, key string) (CachedContent, error) {
	encoded, err := c.store.Get(ctx, key)
	if err != nil {
		return CachedContent{}, err
	}
	var index cachedOCIIndex
	if err := json.Unmarshal(encoded, &index); err != nil {
		_ = c.removeIndex(ctx, key, encoded, cachedOCIIndex{})
		return CachedContent{}, ErrCacheMiss
	}
	if !time.Now().UTC().Before(index.ExpiresAt) {
		_ = c.removeIndex(ctx, key, encoded, index)
		return CachedContent{}, ErrCacheMiss
	}
	if index.Negative {
		return CachedContent{Member: index.Member, Endpoint: index.Endpoint}, ErrNegativeCache
	}
	info, err := c.store.Stat(ctx, index.Object)
	if err != nil {
		_ = c.removeIndex(ctx, key, encoded, index)
		return CachedContent{}, ErrCacheMiss
	}
	if info.Digest == "" {
		if err := c.verifyAndMigrateObject(ctx, index); err != nil {
			_ = c.removeIndex(ctx, key, encoded, index)
			return CachedContent{}, ErrCacheMiss
		}
		info.Digest = index.Digest
	}
	if info.Size != index.Size || info.Digest != index.Digest {
		_ = c.removeIndex(ctx, key, encoded, index)
		return CachedContent{}, ErrCacheMiss
	}
	return CachedContent{Digest: index.Digest, ContentType: index.ContentType, Member: index.Member, Endpoint: index.Endpoint, Object: index.Object, Size: info.Size, store: c.store}, nil
}

func (c *Cache) verifyAndMigrateObject(ctx context.Context, index cachedOCIIndex) error {
	body, size, err := c.store.Open(ctx, index.Object)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, body)
	closeErr := body.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if size != index.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != index.Digest {
		return errors.New("OCI legacy cached object does not match index")
	}
	return c.store.SetVerifiedDigest(ctx, index.Object, index.Digest)
}

func (c *Cache) Store(ctx context.Context, key string, content CachedContent) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		return c.storeContent(workCtx, key, content)
	})
}

// Stage stores a verified response without publishing an index. It is used for
// coalesced Hosted responses: waiters need an independent reader, but Hosted
// content must not become eligible for Proxy fallback cache reads.
func (c *Cache) Stage(ctx context.Context, content CachedContent) (CachedContent, error) {
	object := "oci/objects/" + strings.ReplaceAll(content.Digest, ":", "/")
	if content.tempPath == "" {
		return CachedContent{}, errors.New("OCI staged content has no reader")
	}
	file, err := os.Open(content.tempPath)
	if err != nil {
		return CachedContent{}, err
	}
	err = c.store.PutVerifiedReader(ctx, object, file, content.Size, content.Digest)
	closeErr := file.Close()
	if err != nil {
		return CachedContent{}, err
	}
	if closeErr != nil {
		return CachedContent{}, closeErr
	}
	if err := c.markObjectForCollection(ctx, object); err != nil {
		return CachedContent{}, err
	}
	return CachedContent{Digest: content.Digest, ContentType: content.ContentType, Member: content.Member, Object: object, Size: content.Size, store: c.store}, nil
}

func (c *Cache) storeContent(ctx context.Context, key string, content CachedContent) error {
	if content.Size == 0 && content.tempPath == "" {
		content.Size = int64(len(content.Body))
	}
	return c.quota.Admit(ctx, content.Repository, key, content.Size, func() error {
		return c.storeContentAdmitted(ctx, key, content)
	})
}

func (c *Cache) storeContentAdmitted(ctx context.Context, key string, content CachedContent) error {
	object := "oci/objects/" + strings.ReplaceAll(content.Digest, ":", "/")
	previous := c.loadIndex(ctx, key)
	// Publish the index only after its immutable, digest-addressed object exists.
	if content.tempPath != "" {
		file, err := os.Open(content.tempPath)
		if err != nil {
			return err
		}
		err = c.store.PutVerifiedReader(ctx, object, file, content.Size, content.Digest)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	} else if err := c.store.Put(ctx, object, content.Body); err != nil {
		return err
	}
	encoded, err := json.Marshal(cachedOCIIndex{Object: object, Digest: content.Digest, Repository: content.Repository, ContentType: content.ContentType, Member: content.Member, Endpoint: content.Endpoint, Size: content.Size, ExpiresAt: time.Now().UTC().Add(c.ttl)})
	if err != nil {
		return err
	}
	if err := c.store.Put(ctx, key, encoded); err != nil {
		_ = c.markObjectForCollection(ctx, object)
		return err
	}
	if previous.Object != "" && previous.Object != object {
		_ = c.markObjectForCollection(ctx, previous.Object)
	}
	return nil
}

func (c *Cache) StoreNegative(ctx context.Context, key string, member repository.Member) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		return c.storeNegative(workCtx, key, member)
	})
}

// Invalidate removes an index and schedules its digest object for collection.
// It is used when an index no longer has enough provenance to serve safely.
func (c *Cache) Invalidate(ctx context.Context, key string) {
	encoded, err := c.store.Get(ctx, key)
	if err != nil {
		return
	}
	var index cachedOCIIndex
	if json.Unmarshal(encoded, &index) != nil {
		return
	}
	_ = c.removeIndex(ctx, key, encoded, index)
}

func (c *Cache) storeNegative(ctx context.Context, key string, member repository.Member) error {
	previous := c.loadIndex(ctx, key)
	encoded, err := json.Marshal(cachedOCIIndex{Negative: true, Member: member.Name, Endpoint: member.Endpoint, ExpiresAt: time.Now().UTC().Add(c.negativeTTL)})
	if err != nil {
		return err
	}
	if err := c.store.Put(ctx, key, encoded); err != nil {
		return err
	}
	if previous.Object != "" {
		_ = c.markObjectForCollection(ctx, previous.Object)
	}
	return nil
}

func (c *Cache) loadIndex(ctx context.Context, key string) cachedOCIIndex {
	encoded, err := c.store.Get(ctx, key)
	if err != nil {
		return cachedOCIIndex{}
	}
	var index cachedOCIIndex
	if json.Unmarshal(encoded, &index) != nil {
		return cachedOCIIndex{}
	}
	return index
}

func (c *Cache) removeIndex(ctx context.Context, key string, expected []byte, index cachedOCIIndex) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		return c.removeIndexLocked(workCtx, key, expected, index)
	})
}

func (c *Cache) removeIndexLocked(ctx context.Context, key string, expected []byte, index cachedOCIIndex) error {
	current, err := c.store.Get(ctx, key)
	if err != nil || !bytes.Equal(current, expected) {
		return err
	}
	if err := c.store.Delete(ctx, key); err != nil {
		return err
	}
	if index.Object != "" {
		_ = c.markObjectForCollection(ctx, index.Object)
	}
	return nil
}

func (c *Cache) markObjectForCollection(ctx context.Context, object string) error {
	encoded, err := json.Marshal(ociGarbageCandidate{Object: object, DeleteAfter: time.Now().UTC().Add(c.gcGrace)})
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(object))
	return c.store.Put(ctx, "oci/gc/"+hex.EncodeToString(sum[:])+".json", encoded)
}

// CollectGarbage deletes only candidates whose grace period has elapsed and
// whose digest object is not referenced by any currently valid OCI index.
func (c *Cache) CollectGarbage(ctx context.Context) error {
	return c.withPublicationLock(ctx, c.collectGarbage)
}

func (c *Cache) collectGarbage(ctx context.Context) error {
	garbage, err := c.store.List(ctx, "oci/gc/")
	if err != nil {
		return err
	}
	references, err := c.referencedObjects(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, key := range garbage {
		encoded, err := c.store.Get(ctx, key)
		if err != nil {
			continue
		}
		var candidate ociGarbageCandidate
		if json.Unmarshal(encoded, &candidate) != nil || candidate.Object == "" {
			_ = c.store.Delete(ctx, key)
			continue
		}
		if now.Before(candidate.DeleteAfter) || references[candidate.Object] {
			continue
		}
		if err := c.store.Delete(ctx, candidate.Object); err != nil {
			return err
		}
		_ = c.store.Delete(ctx, key)
	}
	return nil
}

func (c *Cache) withPublicationLock(ctx context.Context, work func(context.Context) error) error {
	c.publicationMu.Lock()
	defer c.publicationMu.Unlock()
	if c.coordinator == nil {
		return work(ctx)
	}
	for {
		owner, acquired, err := c.coordinator.Acquire(ctx, "oci-publication", c.lockLease)
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
			_ = c.coordinator.Release(releaseCtx, "oci-publication", owner)
		}()
		workCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		renewalFailed := make(chan struct{})
		go c.renewOCILock(workCtx, "oci-publication", owner, renewalFailed, cancel)
		err = work(workCtx)
		select {
		case <-renewalFailed:
			return errors.New("OCI distributed publication lock renewal failed")
		default:
			return err
		}
	}
}

func (c *Cache) referencedObjects(ctx context.Context) (map[string]bool, error) {
	keys, err := c.store.List(ctx, "oci/index/")
	if err != nil {
		return nil, err
	}
	references := make(map[string]bool)
	now := time.Now().UTC()
	for _, key := range keys {
		index := c.loadIndex(ctx, key)
		if index.Object != "" && now.Before(index.ExpiresAt) {
			references[index.Object] = true
		}
	}
	return references, nil
}

func (c *Cache) Do(ctx context.Context, key string, fetch func(context.Context) (CachedContent, error)) (CachedContent, error) {
	value, err, _ := c.group.Do(key, func() (any, error) {
		if c.coordinator == nil {
			return fetch(ctx)
		}
		for {
			owner, acquired, err := c.coordinator.Acquire(ctx, key, c.lockLease)
			if err != nil {
				return nil, err
			}
			if acquired {
				defer func() {
					releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					_ = c.coordinator.Release(releaseCtx, key, owner)
				}()
				workCtx, cancel := context.WithCancel(ctx)
				defer cancel()
				renewalFailed := make(chan struct{})
				go c.renewOCILock(workCtx, key, owner, renewalFailed, cancel)
				content, fetchErr := fetch(workCtx)
				select {
				case <-renewalFailed:
					return nil, errors.New("OCI distributed cache lock renewal failed")
				default:
				}
				return content, fetchErr
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(20 * time.Millisecond):
				continue
			}
		}
	})
	if err != nil {
		return CachedContent{}, err
	}
	return value.(CachedContent), nil
}

func (c *Cache) renewOCILock(ctx context.Context, key, owner string, failed chan<- struct{}, cancel context.CancelFunc) {
	ticker := time.NewTicker(c.lockRenewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := c.coordinator.Renew(ctx, key, owner, c.lockLease)
			if err != nil || !ok {
				close(failed)
				cancel()
				return
			}
		}
	}
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
