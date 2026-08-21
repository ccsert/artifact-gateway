package raw

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	cache "github.com/artifact-gateway/artifact-gateway/internal/cache"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

var ErrCacheMiss = errors.New("raw cache miss")
var ErrNegativeCache = errors.New("raw negative cache hit")
var ErrObjectTooLarge = errors.New("raw object exceeds size limit")
var ErrSpoolCapacity = errors.New("raw spool capacity is full")

const defaultCacheTTL = 15 * time.Minute
const defaultNegativeCacheTTL = time.Minute
const defaultMaxObjectBytes = int64(1 << 30)
const distributedLockLease = 35 * time.Second
const streamCopyBufferSize = 128 << 10
const defaultMaxConcurrentSpools = 4

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
	Object                                            string
	Size                                              int64
	tempPath                                          string
	store                                             objectstore.Store
}

// StageContent copies an upstream object into a bounded-buffer temporary file
// while computing its digest. The caller owns Cleanup after the content has
// either been published or served.
func StageContent(reader io.Reader, maxBytes int64) (CachedContent, error) {
	if maxBytes < 0 {
		return CachedContent{}, ErrObjectTooLarge
	}
	file, err := os.CreateTemp("", "artifact-gateway-raw-*")
	if err != nil {
		return CachedContent{}, err
	}
	fail := func(err error) (CachedContent, error) {
		name := file.Name()
		_ = file.Close()
		_ = os.Remove(name)
		return CachedContent{}, err
	}
	limit := maxBytes
	if maxBytes < int64(^uint64(0)>>1) {
		limit++
	}
	hash := sha256.New()
	written, err := io.CopyBuffer(io.MultiWriter(file, hash), io.LimitReader(reader, limit), make([]byte, streamCopyBufferSize))
	if err != nil {
		return fail(err)
	}
	if written > maxBytes {
		return fail(ErrObjectTooLarge)
	}
	if err := file.Close(); err != nil {
		return fail(err)
	}
	return CachedContent{Digest: hex.EncodeToString(hash.Sum(nil)), Size: written, tempPath: file.Name()}, nil
}

func (c CachedContent) Open(ctx context.Context) (io.ReadCloser, int64, error) {
	if c.tempPath != "" {
		file, err := os.Open(c.tempPath)
		if err != nil {
			return nil, 0, err
		}
		return file, c.Size, nil
	}
	if c.Object != "" {
		if c.store == nil {
			return nil, 0, errors.New("raw cached object has no object store")
		}
		return c.store.Open(ctx, c.Object)
	}
	return io.NopCloser(bytes.NewReader(c.Body)), int64(len(c.Body)), nil
}

func (c CachedContent) OpenRange(ctx context.Context, offset, length int64) (io.ReadCloser, int64, error) {
	if offset < 0 || length < 0 || offset > c.Size || length > c.Size-offset {
		return nil, 0, errors.New("raw content range is out of bounds")
	}
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
	if c.Object != "" {
		if c.store == nil {
			return nil, 0, errors.New("raw cached object has no object store")
		}
		return c.store.OpenRange(ctx, c.Object, offset, length)
	}
	if offset > int64(len(c.Body)) || length > int64(len(c.Body))-offset {
		return nil, 0, errors.New("raw content range is out of bounds")
	}
	return io.NopCloser(bytes.NewReader(c.Body[offset : offset+length])), int64(len(c.Body)), nil
}

func (c CachedContent) Cleanup() {
	if c.tempPath != "" {
		_ = os.Remove(c.tempPath)
	}
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
	lockLease        time.Duration
	lockRenewEvery   time.Duration
	publicationMu    sync.Mutex
	spoolSlots       chan struct{}
}

func NewCache(store objectstore.Store, ttl, negativeTTL time.Duration, hosts []string) *Cache {
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = struct{}{}
		}
	}
	return &Cache{store: store, ttl: ttl, negativeTTL: negativeTTL, maxObjectBytes: defaultMaxObjectBytes, allowed: allowed, lockLease: distributedLockLease, lockRenewEvery: distributedLockLease / 3, spoolSlots: make(chan struct{}, defaultMaxConcurrentSpools)}
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

// WithMaxConcurrentSpools bounds the number of cache-miss temporary files
// owned by this Gateway process. Configure it before serving requests.
func (c *Cache) WithMaxConcurrentSpools(limit int) *Cache {
	if limit > 0 {
		c.spoolSlots = make(chan struct{}, limit)
	}
	return c
}

// AcquireSpool reserves one complete cache-miss staging lifecycle. Admission
// is deliberately fail-fast so a saturated Gateway does not start another
// potentially large upstream transfer.
func (c *Cache) AcquireSpool() (func(), error) {
	if c == nil || c.spoolSlots == nil {
		return func() {}, nil
	}
	select {
	case c.spoolSlots <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-c.spoolSlots })
		}, nil
	default:
		return nil, ErrSpoolCapacity
	}
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
	info, err := c.store.Stat(ctx, index.Object)
	if err != nil {
		c.removeIndex(ctx, key, encoded)
		return CachedContent{}, ErrCacheMiss
	}
	wantDigest := "sha256:" + index.Digest
	if info.Digest == "" {
		if err := c.verifyAndMigrateObject(ctx, index); err != nil {
			c.removeIndex(ctx, key, encoded)
			return CachedContent{}, ErrCacheMiss
		}
		info.Digest = wantDigest
	}
	if info.Size != index.Size || info.Digest != wantDigest {
		c.removeIndex(ctx, key, encoded)
		return CachedContent{}, ErrCacheMiss
	}
	return CachedContent{Digest: index.Digest, ContentType: index.ContentType, Member: index.Member, Endpoint: index.Endpoint, Repository: index.Repository, Path: index.Path, Object: index.Object, Size: index.Size, store: c.store}, nil
}

func (c *Cache) Store(ctx context.Context, key string, content CachedContent) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		if content.tempPath == "" {
			content.Size = int64(len(content.Body))
			sum := sha256.Sum256(content.Body)
			content.Digest = hex.EncodeToString(sum[:])
		}
		content.Digest = strings.TrimPrefix(content.Digest, "sha256:")
		if len(content.Digest) != sha256.Size*2 || content.Size < 0 {
			return errors.New("raw cached content is not verified")
		}
		return c.quota.AdmitWithLimit(workCtx, content.Repository, key, content.Size, content.CacheQuotaBytes, func() error {
			digest := content.Digest
			object := "raw/objects/" + digest
			reader, size, err := content.Open(workCtx)
			if err != nil {
				return err
			}
			if size != content.Size {
				_ = reader.Close()
				return fmt.Errorf("raw cached content size mismatch: got %d want %d", size, content.Size)
			}
			putErr := c.store.PutVerifiedReader(workCtx, object, reader, content.Size, "sha256:"+digest)
			closeErr := reader.Close()
			if putErr != nil || closeErr != nil {
				return errors.Join(putErr, closeErr)
			}
			encoded, err := json.Marshal(cacheIndex{Object: object, Digest: digest, ContentType: content.ContentType, Member: content.Member, Endpoint: content.Endpoint, Repository: content.Repository, Path: content.Path, Size: content.Size, ExpiresAt: time.Now().UTC().Add(c.ttl)})
			if err != nil {
				return err
			}
			return c.store.Put(workCtx, key, encoded)
		})
	})
}

func (c *Cache) verifyAndMigrateObject(ctx context.Context, index cacheIndex) error {
	body, size, err := c.store.Open(ctx, index.Object)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, copyErr := io.CopyBuffer(hash, body, make([]byte, streamCopyBufferSize))
	closeErr := body.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if size != index.Size || hex.EncodeToString(hash.Sum(nil)) != index.Digest {
		return errors.New("raw legacy cached object does not match index")
	}
	return c.store.SetVerifiedDigest(ctx, index.Object, "sha256:"+index.Digest)
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

func (c *Cache) AcquireRequestLock(ctx context.Context, key string) (context.Context, func() error, error) {
	if c.coordinator == nil {
		return ctx, func() error { return nil }, nil
	}
	lockKey := "cache-request:" + key
	for {
		owner, acquired, err := c.coordinator.Acquire(ctx, lockKey, c.lockLease)
		if err != nil {
			return nil, nil, err
		}
		if acquired {
			workCtx, cancel := context.WithCancel(ctx)
			renewalFailed := make(chan struct{})
			stopped := make(chan struct{})
			go c.renewRequestLock(workCtx, lockKey, owner, renewalFailed, stopped, cancel)
			var once sync.Once
			var releaseErr error
			release := func() error {
				once.Do(func() {
					cancel()
					<-stopped
					select {
					case <-renewalFailed:
						releaseErr = errors.New("raw distributed request lock renewal failed")
					default:
					}
					releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					releaseErr = errors.Join(releaseErr, c.coordinator.Release(releaseCtx, lockKey, owner))
				})
				return releaseErr
			}
			return workCtx, release, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (c *Cache) renewRequestLock(ctx context.Context, key, owner string, failed chan<- struct{}, stopped chan<- struct{}, cancel context.CancelFunc) {
	defer close(stopped)
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
		owner, acquired, err := c.coordinator.Acquire(ctx, "raw-publication", c.lockLease)
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
			ok, err := c.coordinator.Renew(ctx, "raw-publication", owner, c.lockLease)
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
