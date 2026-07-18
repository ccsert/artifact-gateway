package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

var errOCICacheMiss = errors.New("OCI cache miss")
var errOCICacheNegative = errors.New("OCI negative cache hit")
var errOCIUpstreamOpen = errors.New("OCI upstream circuit is open")

const defaultOCICacheTTL = 15 * time.Minute
const defaultOCINegativeCacheTTL = time.Minute
const defaultOCIProxyBreakerTTL = 30 * time.Second
const ociSharedWorkTimeout = 30 * time.Second
const ociDistributedLockLease = 35 * time.Second
const ociDistributedLockRenewInterval = ociDistributedLockLease / 3

// OCIObjectStore is deliberately small so the cache's publication ordering can
// be tested without a running object-store service.
type OCIObjectStore interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
	Delete(context.Context, string) error
	List(context.Context, string) ([]string, error)
}

type MemoryOCIObjectStore struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

func NewMemoryOCIObjectStore() *MemoryOCIObjectStore {
	return &MemoryOCIObjectStore{objects: make(map[string][]byte)}
}

func (s *MemoryOCIObjectStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.objects[key]
	if !ok {
		return nil, errOCICacheMiss
	}
	return append([]byte(nil), value...), nil
}

func (s *MemoryOCIObjectStore) Put(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), value...)
	return nil
}

func (s *MemoryOCIObjectStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *MemoryOCIObjectStore) List(_ context.Context, prefix string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	keys := make([]string, 0)
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

type S3OCIObjectStore struct {
	client *minio.Client
	bucket string
}

func NewS3OCIObjectStore(endpoint, accessKey, secretKey, bucket string) (*S3OCIObjectStore, error) {
	client, err := minio.New(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: strings.HasPrefix(endpoint, "https://"),
	})
	if err != nil {
		return nil, err
	}
	return &S3OCIObjectStore{client: client, bucket: bucket}, nil
}

func (s *S3OCIObjectStore) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil || exists {
		return err
	}
	return s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
}

func (s *S3OCIObjectStore) Get(ctx context.Context, key string) ([]byte, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = object.Close() }()
	value, err := io.ReadAll(object)
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return nil, errOCICacheMiss
	}
	return value, err
}

func (s *S3OCIObjectStore) Put(ctx context.Context, key string, value []byte) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, strings.NewReader(string(value)), int64(len(value)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

func (s *S3OCIObjectStore) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *S3OCIObjectStore) List(ctx context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0)
	for object := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return nil, object.Err
		}
		keys = append(keys, object.Key)
	}
	return keys, nil
}

type cachedOCIIndex struct {
	Object      string    `json:"object,omitempty"`
	Digest      string    `json:"digest,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Member      string    `json:"member,omitempty"`
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
	lockLease        time.Duration
	lockRenewEvery   time.Duration
	gcGrace          time.Duration
}

type OCICacheCoordinator interface {
	Acquire(context.Context, string, time.Duration) (string, bool, error)
	Renew(context.Context, string, string, time.Duration) (bool, error)
	Release(context.Context, string, string) error
	CircuitOpen(context.Context, string) (bool, error)
	OpenCircuit(context.Context, string, time.Duration) error
	CloseCircuit(context.Context, string) error
}

type RedisOCICacheCoordinator struct{ client *redis.Client }

func NewRedisOCICacheCoordinator(address string) *RedisOCICacheCoordinator {
	return &RedisOCICacheCoordinator{client: redis.NewClient(&redis.Options{Addr: address})}
}

func (c *RedisOCICacheCoordinator) Acquire(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	owner, err := newOCILockOwner()
	if err != nil {
		return "", false, err
	}
	ok, err := c.client.SetNX(ctx, "artifact-gateway:oci:lock:"+key, owner, ttl).Result()
	return owner, ok, err
}

func (c *RedisOCICacheCoordinator) Renew(ctx context.Context, key, owner string, ttl time.Duration) (bool, error) {
	result, err := c.client.Eval(ctx, "if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('pexpire', KEYS[1], ARGV[2]) end return 0", []string{ociLockKey(key)}, owner, ttl.Milliseconds()).Int()
	return result == 1, err
}

func (c *RedisOCICacheCoordinator) Release(ctx context.Context, key, owner string) error {
	_, err := c.client.Eval(ctx, "if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) end return 0", []string{ociLockKey(key)}, owner).Result()
	return err
}

func ociLockKey(key string) string { return "artifact-gateway:oci:lock:" + key }

func newOCILockOwner() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
func (c *RedisOCICacheCoordinator) CircuitOpen(ctx context.Context, key string) (bool, error) {
	result, err := c.client.Exists(ctx, "artifact-gateway:oci:circuit:"+key).Result()
	return result == 1, err
}
func (c *RedisOCICacheCoordinator) OpenCircuit(ctx context.Context, key string, ttl time.Duration) error {
	return c.client.Set(ctx, "artifact-gateway:oci:circuit:"+key, "1", ttl).Err()
}
func (c *RedisOCICacheCoordinator) CloseCircuit(ctx context.Context, key string) error {
	return c.client.Del(ctx, "artifact-gateway:oci:circuit:"+key).Err()
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
		_ = c.removeIndex(ctx, key, cachedOCIIndex{})
		return CachedOCIContent{}, errOCICacheMiss
	}
	if !time.Now().UTC().Before(index.ExpiresAt) {
		_ = c.removeIndex(ctx, key, index)
		return CachedOCIContent{}, errOCICacheMiss
	}
	if index.Negative {
		return CachedOCIContent{}, errOCICacheNegative
	}
	body, err := c.store.Get(ctx, index.Object)
	if err != nil {
		_ = c.removeIndex(ctx, key, index)
		return CachedOCIContent{}, errOCICacheMiss
	}
	sum := sha256.Sum256(body)
	if "sha256:"+hex.EncodeToString(sum[:]) != index.Digest {
		_ = c.removeIndex(ctx, key, index)
		_ = c.store.Delete(ctx, index.Object)
		return CachedOCIContent{}, errOCICacheMiss
	}
	return CachedOCIContent{Body: body, Digest: index.Digest, ContentType: index.ContentType, Member: index.Member}, nil
}

func (c *OCICache) Store(ctx context.Context, key string, content CachedOCIContent) error {
	object := "oci/objects/" + strings.ReplaceAll(content.Digest, ":", "/")
	previous := c.loadIndex(ctx, key)
	// Publish the index only after its immutable, digest-addressed object exists.
	if err := c.store.Put(ctx, object, content.Body); err != nil {
		return err
	}
	encoded, err := json.Marshal(cachedOCIIndex{Object: object, Digest: content.Digest, ContentType: content.ContentType, Member: content.Member, ExpiresAt: time.Now().UTC().Add(c.ttl)})
	if err != nil {
		return err
	}
	if err := c.store.Put(ctx, key, encoded); err != nil {
		return err
	}
	if previous.Object != "" && previous.Object != object {
		_ = c.markObjectForCollection(ctx, previous.Object)
	}
	_ = c.CollectGarbage(ctx)
	return nil
}

func (c *OCICache) StoreNegative(ctx context.Context, key string) error {
	previous := c.loadIndex(ctx, key)
	encoded, err := json.Marshal(cachedOCIIndex{Negative: true, ExpiresAt: time.Now().UTC().Add(c.negativeTTL)})
	if err != nil {
		return err
	}
	if err := c.store.Put(ctx, key, encoded); err != nil {
		return err
	}
	if previous.Object != "" {
		_ = c.markObjectForCollection(ctx, previous.Object)
	}
	_ = c.CollectGarbage(ctx)
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

func (c *OCICache) removeIndex(ctx context.Context, key string, index cachedOCIIndex) error {
	if err := c.store.Delete(ctx, key); err != nil {
		return err
	}
	if index.Object != "" {
		_ = c.markObjectForCollection(ctx, index.Object)
	}
	_ = c.CollectGarbage(ctx)
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
				defer func() { _ = c.coordinator.Release(context.Background(), key, owner) }()
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
