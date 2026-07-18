package app

import (
	"context"
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
	"golang.org/x/sync/singleflight"
)

var errOCICacheMiss = errors.New("OCI cache miss")
var errOCICacheNegative = errors.New("OCI negative cache hit")
var errOCIUpstreamOpen = errors.New("OCI upstream circuit is open")

const defaultOCICacheTTL = 15 * time.Minute
const defaultOCINegativeCacheTTL = time.Minute
const defaultOCIProxyBreakerTTL = 30 * time.Second

// OCIObjectStore is deliberately small so the cache's publication ordering can
// be tested without a running object-store service.
type OCIObjectStore interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
	Delete(context.Context, string) error
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

type cachedOCIIndex struct {
	Object      string    `json:"object,omitempty"`
	Digest      string    `json:"digest,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Member      string    `json:"member,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
	Negative    bool      `json:"negative,omitempty"`
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
}

func NewOCICache(store OCIObjectStore, ttl, negativeTTL, breakerTTL time.Duration, allowedProxyHosts []string) *OCICache {
	allowed := make(map[string]struct{}, len(allowedProxyHosts))
	for _, host := range allowedProxyHosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = struct{}{}
		}
	}
	return &OCICache{store: store, ttl: ttl, negativeTTL: negativeTTL, breakerTTL: breakerTTL, allowedProxyHost: allowed, openUntil: make(map[string]time.Time)}
}

func NewDefaultOCICache(store OCIObjectStore, allowedProxyHosts []string) *OCICache {
	return NewOCICache(store, defaultOCICacheTTL, defaultOCINegativeCacheTTL, defaultOCIProxyBreakerTTL, allowedProxyHosts)
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
		_ = c.store.Delete(ctx, key)
		return CachedOCIContent{}, errOCICacheMiss
	}
	if !time.Now().UTC().Before(index.ExpiresAt) {
		_ = c.store.Delete(ctx, key)
		return CachedOCIContent{}, errOCICacheMiss
	}
	if index.Negative {
		return CachedOCIContent{}, errOCICacheNegative
	}
	body, err := c.store.Get(ctx, index.Object)
	if err != nil {
		_ = c.store.Delete(ctx, key)
		return CachedOCIContent{}, errOCICacheMiss
	}
	return CachedOCIContent{Body: body, Digest: index.Digest, ContentType: index.ContentType, Member: index.Member}, nil
}

func (c *OCICache) Store(ctx context.Context, key string, content CachedOCIContent) error {
	object := "oci/objects/" + strings.ReplaceAll(content.Digest, ":", "/")
	// Publish the index only after its immutable, digest-addressed object exists.
	if err := c.store.Put(ctx, object, content.Body); err != nil {
		return err
	}
	encoded, err := json.Marshal(cachedOCIIndex{Object: object, Digest: content.Digest, ContentType: content.ContentType, Member: content.Member, ExpiresAt: time.Now().UTC().Add(c.ttl)})
	if err != nil {
		return err
	}
	return c.store.Put(ctx, key, encoded)
}

func (c *OCICache) StoreNegative(ctx context.Context, key string) error {
	encoded, err := json.Marshal(cachedOCIIndex{Negative: true, ExpiresAt: time.Now().UTC().Add(c.negativeTTL)})
	if err != nil {
		return err
	}
	return c.store.Put(ctx, key, encoded)
}

func (c *OCICache) Do(key string, fetch func() (CachedOCIContent, error)) (CachedOCIContent, error) {
	value, err, _ := c.group.Do(key, func() (any, error) { return fetch() })
	if err != nil {
		return CachedOCIContent{}, err
	}
	return value.(CachedOCIContent), nil
}

func (c *OCICache) ProxyAllowed(endpoint string) bool {
	if len(c.allowedProxyHost) == 0 {
		return false
	}
	host := strings.ToLower(strings.Split(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), "/")[0])
	_, ok := c.allowedProxyHost[host]
	return ok
}

func (c *OCICache) UpstreamAllowed(endpoint string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	until := c.openUntil[endpoint]
	return until.IsZero() || !time.Now().UTC().Before(until)
}

func (c *OCICache) RecordUpstreamFailure(endpoint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.openUntil[endpoint] = time.Now().UTC().Add(c.breakerTTL)
}

func (c *OCICache) RecordUpstreamSuccess(endpoint string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.openUntil, endpoint)
}
