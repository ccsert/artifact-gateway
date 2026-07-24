package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
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
const ociSharedWorkTimeout = 30 * time.Second
const ociDistributedLockLease = 35 * time.Second
const ociDistributedLockRenewInterval = ociDistributedLockLease / 3

// OCIObjectStore is deliberately small so the cache's publication ordering can
// be tested without a running object-store service.
type OCIObjectStore interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
	Stat(context.Context, string) (OCIObjectInfo, error)
	Open(context.Context, string) (io.ReadCloser, int64, error)
	OpenRange(context.Context, string, int64, int64) (io.ReadCloser, int64, error)
	PutReader(context.Context, string, io.Reader, int64) error
	PutVerifiedReader(context.Context, string, io.Reader, int64, string) error
	SetVerifiedDigest(context.Context, string, string) error
	Delete(context.Context, string) error
	List(context.Context, string) ([]string, error)
}

type OCIObjectInfo struct {
	Size   int64
	Digest string
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

func (s *MemoryOCIObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	value, err := s.Get(ctx, key)
	if err != nil {
		return nil, 0, err
	}
	return io.NopCloser(bytes.NewReader(value)), int64(len(value)), nil
}

func (s *MemoryOCIObjectStore) Stat(ctx context.Context, key string) (OCIObjectInfo, error) {
	value, err := s.Get(ctx, key)
	if err != nil {
		return OCIObjectInfo{}, err
	}
	sum := sha256.Sum256(value)
	return OCIObjectInfo{Size: int64(len(value)), Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

func (s *MemoryOCIObjectStore) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	value, err := s.Get(ctx, key)
	if err != nil {
		return nil, 0, err
	}
	size := int64(len(value))
	if offset < 0 || length < 0 || offset > size || length > size-offset {
		return nil, 0, errors.New("OCI object range is out of bounds")
	}
	return io.NopCloser(bytes.NewReader(value[offset : offset+length])), size, nil
}

func (s *MemoryOCIObjectStore) PutReader(ctx context.Context, key string, value io.Reader, _ int64) error {
	data, err := io.ReadAll(value)
	if err != nil {
		return err
	}
	return s.Put(ctx, key, data)
}

func (s *MemoryOCIObjectStore) PutVerifiedReader(ctx context.Context, key string, value io.Reader, size int64, digest string) error {
	return s.PutReader(ctx, key, value, size)
}

func (s *MemoryOCIObjectStore) SetVerifiedDigest(context.Context, string, string) error { return nil }

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
	return s.PutReader(ctx, key, bytes.NewReader(value), int64(len(value)))
}

func (s *S3OCIObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, errOCICacheMiss
		}
		return nil, 0, err
	}
	return object, info.Size, nil
}

func (s *S3OCIObjectStore) Stat(ctx context.Context, key string) (OCIObjectInfo, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return OCIObjectInfo{}, errOCICacheMiss
		}
		return OCIObjectInfo{}, err
	}
	return OCIObjectInfo{Size: info.Size, Digest: ociObjectDigestMetadata(info.UserMetadata)}, nil
}

func ociObjectDigestMetadata(metadata map[string]string) string {
	for key, value := range metadata {
		if strings.EqualFold(key, "Artifact-Gateway-Sha256") || strings.EqualFold(key, "X-Amz-Meta-Artifact-Gateway-Sha256") {
			return value
		}
	}
	return ""
}

func (s *S3OCIObjectStore) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, errOCICacheMiss
		}
		return nil, 0, err
	}
	if offset < 0 || length < 0 || offset > info.Size || length > info.Size-offset {
		return nil, 0, errors.New("OCI object range is out of bounds")
	}
	options := minio.GetObjectOptions{}
	if err := options.SetRange(offset, offset+length-1); err != nil {
		return nil, 0, err
	}
	object, err := s.client.GetObject(ctx, s.bucket, key, options)
	if err != nil {
		return nil, 0, err
	}
	return object, info.Size, nil
}

func (s *S3OCIObjectStore) PutReader(ctx context.Context, key string, value io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, value, size, minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

func (s *S3OCIObjectStore) PutVerifiedReader(ctx context.Context, key string, value io.Reader, size int64, digest string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, value, size, minio.PutObjectOptions{ContentType: "application/octet-stream", UserMetadata: map[string]string{"Artifact-Gateway-Sha256": digest}})
	return err
}

func (s *S3OCIObjectStore) SetVerifiedDigest(ctx context.Context, key, digest string) error {
	_, err := s.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: s.bucket, Object: key, ReplaceMetadata: true, UserMetadata: map[string]string{"Artifact-Gateway-Sha256": digest}},
		minio.CopySrcOptions{Bucket: s.bucket, Object: key},
	)
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

type OCICacheCoordinator interface {
	Acquire(context.Context, string, time.Duration) (string, bool, error)
	Renew(context.Context, string, string, time.Duration) (bool, error)
	Release(context.Context, string, string) error
	CircuitOpen(context.Context, string) (bool, error)
	OpenCircuit(context.Context, string, time.Duration) error
	CloseCircuit(context.Context, string) error
}

func newOCILockOwner() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
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
