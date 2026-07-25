// Package objectstore defines the byte-store port shared by protocol caches
// and native artifact lifecycles.
package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound is returned when a requested object is absent.
var ErrNotFound = errors.New("object not found")

// Store is deliberately small so publication ordering can be tested without
// a running object-store service.
type Store interface {
	Get(context.Context, string) ([]byte, error)
	Put(context.Context, string, []byte) error
	Stat(context.Context, string) (Info, error)
	Open(context.Context, string) (io.ReadCloser, int64, error)
	OpenRange(context.Context, string, int64, int64) (io.ReadCloser, int64, error)
	PutReader(context.Context, string, io.Reader, int64) error
	PutVerifiedReader(context.Context, string, io.Reader, int64, string) error
	SetVerifiedDigest(context.Context, string, string) error
	Delete(context.Context, string) error
	List(context.Context, string) ([]string, error)
}

type Info struct {
	Size   int64
	Digest string
}

type MemoryStore struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{objects: make(map[string][]byte)}
}

func (s *MemoryStore) Get(_ context.Context, key string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *MemoryStore) Put(_ context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), value...)
	return nil
}

func (s *MemoryStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	value, err := s.Get(ctx, key)
	if err != nil {
		return nil, 0, err
	}
	return io.NopCloser(bytes.NewReader(value)), int64(len(value)), nil
}

func (s *MemoryStore) Stat(ctx context.Context, key string) (Info, error) {
	value, err := s.Get(ctx, key)
	if err != nil {
		return Info{}, err
	}
	sum := sha256.Sum256(value)
	return Info{Size: int64(len(value)), Digest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}

func (s *MemoryStore) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	value, err := s.Get(ctx, key)
	if err != nil {
		return nil, 0, err
	}
	size := int64(len(value))
	if offset < 0 || length < 0 || offset > size || length > size-offset {
		return nil, 0, errors.New("object range is out of bounds")
	}
	return io.NopCloser(bytes.NewReader(value[offset : offset+length])), size, nil
}

func (s *MemoryStore) PutReader(ctx context.Context, key string, value io.Reader, _ int64) error {
	data, err := io.ReadAll(value)
	if err != nil {
		return err
	}
	return s.Put(ctx, key, data)
}

func (s *MemoryStore) PutVerifiedReader(ctx context.Context, key string, value io.Reader, size int64, _ string) error {
	return s.PutReader(ctx, key, value, size)
}

func (*MemoryStore) SetVerifiedDigest(context.Context, string, string) error { return nil }

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

func (s *MemoryStore) List(_ context.Context, prefix string) ([]string, error) {
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

type S3Store struct {
	client *minio.Client
	bucket string
}

func NewS3Store(endpoint, accessKey, secretKey, bucket string) (*S3Store, error) {
	client, err := minio.New(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: strings.HasPrefix(endpoint, "https://"),
	})
	if err != nil {
		return nil, err
	}
	return &S3Store{client: client, bucket: bucket}, nil
}

func (s *S3Store) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil || exists {
		return err
	}
	return s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{})
}

func (s *S3Store) Get(ctx context.Context, key string) ([]byte, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = object.Close() }()
	value, err := io.ReadAll(object)
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return nil, ErrNotFound
	}
	return value, err
}

func (s *S3Store) Put(ctx context.Context, key string, value []byte) error {
	return s.PutReader(ctx, key, bytes.NewReader(value), int64(len(value)))
}

func (s *S3Store) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return object, info.Size, nil
}

func (s *S3Store) Stat(ctx context.Context, key string) (Info, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return Info{}, ErrNotFound
		}
		return Info{}, err
	}
	return Info{Size: info.Size, Digest: digestMetadata(info.UserMetadata)}, nil
}

func (s *S3Store) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	if offset < 0 || length < 0 || offset > info.Size || length > info.Size-offset {
		return nil, 0, errors.New("object range is out of bounds")
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

func (s *S3Store) PutReader(ctx context.Context, key string, value io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, value, size, minio.PutObjectOptions{ContentType: "application/octet-stream"})
	return err
}

func (s *S3Store) PutVerifiedReader(ctx context.Context, key string, value io.Reader, size int64, digest string) error {
	_, err := s.client.PutObject(ctx, s.bucket, key, value, size, minio.PutObjectOptions{ContentType: "application/octet-stream", UserMetadata: map[string]string{"Artifact-Gateway-Sha256": digest}})
	return err
}

func (s *S3Store) SetVerifiedDigest(ctx context.Context, key, digest string) error {
	_, err := s.client.CopyObject(ctx,
		minio.CopyDestOptions{Bucket: s.bucket, Object: key, ReplaceMetadata: true, UserMetadata: map[string]string{"Artifact-Gateway-Sha256": digest}},
		minio.CopySrcOptions{Bucket: s.bucket, Object: key},
	)
	return err
}

func (s *S3Store) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

func (s *S3Store) List(ctx context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0)
	for object := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if object.Err != nil {
			return nil, object.Err
		}
		keys = append(keys, object.Key)
	}
	return keys, nil
}

func digestMetadata(metadata map[string]string) string {
	for key, value := range metadata {
		if strings.EqualFold(key, "Artifact-Gateway-Sha256") || strings.EqualFold(key, "X-Amz-Meta-Artifact-Gateway-Sha256") {
			return value
		}
	}
	return ""
}
