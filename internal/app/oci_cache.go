package app

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

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"golang.org/x/sync/singleflight"
)

var errOCICacheMiss = objectstore.ErrNotFound
var errOCICacheNegative = errors.New("OCI negative cache hit")
var errOCIUpstreamOpen = errors.New("OCI upstream circuit is open")

const defaultOCICacheTTL = 15 * time.Minute
const defaultOCINegativeCacheTTL = time.Minute
const defaultOCIProxyBreakerTTL = 30 * time.Second
const ociSharedWorkTimeout = 30 * time.Second
const ociDistributedLockLease = 35 * time.Second
const ociDistributedLockRenewInterval = ociDistributedLockLease / 3

// Compatibility aliases retain the public app composition API while object
// storage is owned by its protocol-independent module.
type OCIObjectStore = objectstore.Store
type OCIObjectInfo = objectstore.Info
type MemoryOCIObjectStore = objectstore.MemoryStore
type S3OCIObjectStore = objectstore.S3Store

var NewMemoryOCIObjectStore = objectstore.NewMemoryStore
var NewS3OCIObjectStore = objectstore.NewS3Store

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

type CachedOCIContent struct {
	Body        []byte
	Digest      string
	ContentType string
	Member      string
	Endpoint    string
	Repository  string
	Object      string
	Size        int64
	tempPath    string
	store       OCIObjectStore
	cacheable   bool
}

type OCICache struct {
	store            OCIObjectStore
	ttl              time.Duration
	negativeTTL      time.Duration
	breakerTTL       time.Duration
	allowedProxyHost map[string]struct{}
	group            singleflight.Group
	mu               sync.Mutex
	openUntil        map[string]time.Time
	coordinator      OCICacheCoordinator
	quota            *CacheQuota
	lockLease        time.Duration
	lockRenewEvery   time.Duration
	gcGrace          time.Duration
	publicationMu    sync.Mutex
}

func NewOCICache(store OCIObjectStore, ttl, negativeTTL, breakerTTL time.Duration, allowedProxyHosts []string) *OCICache {
	allowed := make(map[string]struct{}, len(allowedProxyHosts))
	for _, host := range allowedProxyHosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = struct{}{}
		}
	}
	return &OCICache{store: store, ttl: ttl, negativeTTL: negativeTTL, breakerTTL: breakerTTL, allowedProxyHost: allowed, openUntil: make(map[string]time.Time), lockLease: ociDistributedLockLease, lockRenewEvery: ociDistributedLockRenewInterval, gcGrace: ttl}
}

func NewDefaultOCICache(store OCIObjectStore, allowedProxyHosts []string) *OCICache {
	return NewOCICache(store, defaultOCICacheTTL, defaultOCINegativeCacheTTL, defaultOCIProxyBreakerTTL, allowedProxyHosts)
}

func (c *OCICache) WithCoordinator(coordinator OCICacheCoordinator) *OCICache {
	c.coordinator = coordinator
	return c
}

func (c *OCICache) WithQuota(quota *CacheQuota) *OCICache {
	c.quota = quota
	return c
}

func (c *OCICache) key(group, repository, resource, reference string) string {
	sum := sha256.Sum256([]byte(group + "\x00" + repository + "\x00" + resource + "\x00" + reference))
	return "oci/index/" + hex.EncodeToString(sum[:]) + ".json"
}

func (c *OCICache) Load(ctx context.Context, key string) (CachedOCIContent, error) {
	encoded, err := c.store.Get(ctx, key)
	if err != nil {
		return CachedOCIContent{}, err
	}
	var index cachedOCIIndex
	if err := json.Unmarshal(encoded, &index); err != nil {
		_ = c.removeIndex(ctx, key, encoded, cachedOCIIndex{})
		return CachedOCIContent{}, errOCICacheMiss
	}
	if !time.Now().UTC().Before(index.ExpiresAt) {
		_ = c.removeIndex(ctx, key, encoded, index)
		return CachedOCIContent{}, errOCICacheMiss
	}
	if index.Negative {
		return CachedOCIContent{Member: index.Member, Endpoint: index.Endpoint}, errOCICacheNegative
	}
	info, err := c.store.Stat(ctx, index.Object)
	if err != nil {
		_ = c.removeIndex(ctx, key, encoded, index)
		return CachedOCIContent{}, errOCICacheMiss
	}
	if info.Digest == "" {
		if err := c.verifyAndMigrateObject(ctx, index); err != nil {
			_ = c.removeIndex(ctx, key, encoded, index)
			return CachedOCIContent{}, errOCICacheMiss
		}
		info.Digest = index.Digest
	}
	if info.Size != index.Size || info.Digest != index.Digest {
		_ = c.removeIndex(ctx, key, encoded, index)
		return CachedOCIContent{}, errOCICacheMiss
	}
	return CachedOCIContent{Digest: index.Digest, ContentType: index.ContentType, Member: index.Member, Endpoint: index.Endpoint, Object: index.Object, Size: info.Size, store: c.store}, nil
}

func (c *OCICache) verifyAndMigrateObject(ctx context.Context, index cachedOCIIndex) error {
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

func (c *OCICache) Store(ctx context.Context, key string, content CachedOCIContent) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		return c.storeContent(workCtx, key, content)
	})
}

// Stage stores a verified response without publishing an index. It is used for
// coalesced Hosted responses: waiters need an independent reader, but Hosted
// content must not become eligible for Proxy fallback cache reads.
func (c *OCICache) Stage(ctx context.Context, content CachedOCIContent) (CachedOCIContent, error) {
	object := "oci/objects/" + strings.ReplaceAll(content.Digest, ":", "/")
	if content.tempPath == "" {
		return CachedOCIContent{}, errors.New("OCI staged content has no reader")
	}
	file, err := os.Open(content.tempPath)
	if err != nil {
		return CachedOCIContent{}, err
	}
	err = c.store.PutVerifiedReader(ctx, object, file, content.Size, content.Digest)
	closeErr := file.Close()
	if err != nil {
		return CachedOCIContent{}, err
	}
	if closeErr != nil {
		return CachedOCIContent{}, closeErr
	}
	if err := c.markObjectForCollection(ctx, object); err != nil {
		return CachedOCIContent{}, err
	}
	return CachedOCIContent{Digest: content.Digest, ContentType: content.ContentType, Member: content.Member, Object: object, Size: content.Size, store: c.store}, nil
}

func (c *OCICache) storeContent(ctx context.Context, key string, content CachedOCIContent) error {
	if content.Size == 0 && content.tempPath == "" {
		content.Size = int64(len(content.Body))
	}
	return c.quota.Admit(ctx, content.Repository, key, content.Size, func() error {
		return c.storeContentAdmitted(ctx, key, content)
	})
}

func (c *OCICache) storeContentAdmitted(ctx context.Context, key string, content CachedOCIContent) error {
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

func (c *OCICache) StoreNegative(ctx context.Context, key string, member repository.Member) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		return c.storeNegative(workCtx, key, member)
	})
}

// Invalidate removes an index and schedules its digest object for collection.
// It is used when an index no longer has enough provenance to serve safely.
func (c *OCICache) Invalidate(ctx context.Context, key string) {
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

func (c *OCICache) storeNegative(ctx context.Context, key string, member repository.Member) error {
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

func (c *OCICache) loadIndex(ctx context.Context, key string) cachedOCIIndex {
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

func (c *OCICache) removeIndex(ctx context.Context, key string, expected []byte, index cachedOCIIndex) error {
	return c.withPublicationLock(ctx, func(workCtx context.Context) error {
		return c.removeIndexLocked(workCtx, key, expected, index)
	})
}

func (c *OCICache) removeIndexLocked(ctx context.Context, key string, expected []byte, index cachedOCIIndex) error {
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

func (c *OCICache) markObjectForCollection(ctx context.Context, object string) error {
	encoded, err := json.Marshal(ociGarbageCandidate{Object: object, DeleteAfter: time.Now().UTC().Add(c.gcGrace)})
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(object))
	return c.store.Put(ctx, "oci/gc/"+hex.EncodeToString(sum[:])+".json", encoded)
}

// CollectGarbage deletes only candidates whose grace period has elapsed and
// whose digest object is not referenced by any currently valid OCI index.
func (c *OCICache) CollectGarbage(ctx context.Context) error {
	return c.withPublicationLock(ctx, c.collectGarbage)
}

func (c *OCICache) collectGarbage(ctx context.Context) error {
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

func (c *OCICache) withPublicationLock(ctx context.Context, work func(context.Context) error) error {
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

func (c *OCICache) referencedObjects(ctx context.Context) (map[string]bool, error) {
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

func (c *OCICache) Do(ctx context.Context, key string, fetch func(context.Context) (CachedOCIContent, error)) (CachedOCIContent, error) {
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
		return CachedOCIContent{}, err
	}
	return value.(CachedOCIContent), nil
}

func (c *OCICache) renewOCILock(ctx context.Context, key, owner string, failed chan<- struct{}, cancel context.CancelFunc) {
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

func (c *OCICache) ProxyAllowed(endpoint string) bool {
	if len(c.allowedProxyHost) == 0 {
		return false
	}
	host := strings.ToLower(strings.Split(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/")[0])
	_, ok := c.allowedProxyHost[host]
	return ok
}

func (c *OCICache) UpstreamAllowed(ctx context.Context, endpoint string) bool {
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

func (c *OCICache) RecordUpstreamFailure(ctx context.Context, endpoint string) {
	if c.coordinator != nil {
		_ = c.coordinator.OpenCircuit(ctx, endpoint, c.breakerTTL)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openUntil[endpoint] = time.Now().UTC().Add(c.breakerTTL)
}

func (c *OCICache) RecordUpstreamSuccess(ctx context.Context, endpoint string) {
	if c.coordinator != nil {
		_ = c.coordinator.CloseCircuit(ctx, endpoint)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.openUntil, endpoint)
}
