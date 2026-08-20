// Package objectstore defines the byte-store port shared by protocol caches
// and native artifact lifecycles.
package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
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

type RustFSStore struct {
	client *s3.Client
	bucket string
}

func NewRustFSStore(endpoint, accessKey, secretKey, bucket string) (*RustFSStore, error) {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" ||
		strings.TrimSpace(accessKey) == "" || strings.TrimSpace(secretKey) == "" || strings.TrimSpace(bucket) == "" {
		return nil, errors.New("RustFS endpoint, bucket, and credentials are invalid")
	}
	config := aws.Config{
		Region:      "us-east-1",
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	}
	client := s3.NewFromConfig(config, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(parsed.String())
		options.UsePathStyle = true
	})
	return &RustFSStore{client: client, bucket: bucket}, nil
}

func (s *RustFSStore) EnsureBucket(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); err == nil {
		return nil
	} else if !rustFSNotFound(err) {
		return err
	}
	if _, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)}); err == nil {
		return nil
	} else if _, headErr := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); headErr == nil {
		return nil
	} else {
		return err
	}
}

// CheckBucket verifies that the configured bucket is accessible without
// creating or modifying it.
func (s *RustFSStore) CheckBucket(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); rustFSNotFound(err) {
		return errors.New("configured bucket does not exist")
	} else {
		return err
	}
}

func (s *RustFSStore) Get(ctx context.Context, key string) ([]byte, error) {
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if rustFSNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer func() { _ = object.Body.Close() }()
	value, err := io.ReadAll(object.Body)
	return value, err
}

func (s *RustFSStore) Put(ctx context.Context, key string, value []byte) error {
	return s.PutReader(ctx, key, bytes.NewReader(value), int64(len(value)))
}

func (s *RustFSStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if rustFSNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return object.Body, aws.ToInt64(object.ContentLength), nil
}

func (s *RustFSStore) Stat(ctx context.Context, key string) (Info, error) {
	info, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if rustFSNotFound(err) {
			return Info{}, ErrNotFound
		}
		return Info{}, err
	}
	return Info{Size: aws.ToInt64(info.ContentLength), Digest: digestMetadata(info.Metadata)}, nil
}

func (s *RustFSStore) OpenRange(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	info, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if rustFSNotFound(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	size := aws.ToInt64(info.ContentLength)
	if offset < 0 || length < 0 || offset > size || length > size-offset {
		return nil, 0, errors.New("object range is out of bounds")
	}
	if length == 0 {
		return io.NopCloser(bytes.NewReader(nil)), size, nil
	}
	object, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Range: aws.String(fmt.Sprintf("bytes=%d-%d", offset, offset+length-1)),
	})
	if err != nil {
		return nil, 0, err
	}
	return object.Body, size, nil
}

func (s *RustFSStore) PutReader(ctx context.Context, key string, value io.Reader, size int64) error {
	body, cleanup, err := seekableObjectReader(value, size)
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size), ContentType: aws.String("application/octet-stream"),
	})
	return err
}

func (s *RustFSStore) PutVerifiedReader(ctx context.Context, key string, value io.Reader, size int64, digest string) error {
	body, cleanup, err := seekableObjectReader(value, size)
	if err != nil {
		return err
	}
	defer cleanup()
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size), ContentType: aws.String("application/octet-stream"),
		Metadata: map[string]string{"artifact-gateway-sha256": digest},
	})
	return err
}

func seekableObjectReader(value io.Reader, size int64) (io.ReadSeeker, func(), error) {
	if size < 0 {
		return nil, func() {}, errors.New("object size is invalid")
	}
	if seeker, ok := value.(io.ReadSeeker); ok {
		return seeker, func() {}, nil
	}
	spool, err := os.CreateTemp("", "artifact-gateway-object-*")
	if err != nil {
		return nil, func() {}, err
	}
	spoolPath := spool.Name()
	// Unix keeps the open descriptor usable after unlink and guarantees the
	// spool is reclaimed if the worker crashes. Platforms that cannot unlink an
	// open file fall back to removing the named file during normal cleanup.
	unlinked := os.Remove(spoolPath) == nil
	cleanup := func() {
		_ = spool.Close()
		if !unlinked {
			_ = os.Remove(spoolPath)
		}
	}
	written, copyErr := io.Copy(spool, io.LimitReader(value, size+1))
	if copyErr != nil {
		cleanup()
		return nil, func() {}, copyErr
	}
	if written != size {
		cleanup()
		return nil, func() {}, fmt.Errorf("object reader size mismatch: got %d want %d", written, size)
	}
	if _, err = spool.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return spool, cleanup, nil
}

func (s *RustFSStore) SetVerifiedDigest(ctx context.Context, key, digest string) error {
	info, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if rustFSNotFound(err) {
			return ErrNotFound
		}
		return err
	}
	metadata := make(map[string]string, len(info.Metadata)+1)
	for name, value := range info.Metadata {
		metadata[name] = value
	}
	metadata["artifact-gateway-sha256"] = digest
	_, err = s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), CopySource: aws.String(url.PathEscape(s.bucket + "/" + key)),
		MetadataDirective: types.MetadataDirectiveReplace, Metadata: metadata,
		CacheControl: info.CacheControl, ContentDisposition: info.ContentDisposition, ContentEncoding: info.ContentEncoding,
		ContentLanguage: info.ContentLanguage, ContentType: info.ContentType,
	})
	return err
}

func (s *RustFSStore) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	return err
}

func (s *RustFSStore) List(ctx context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0)
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(s.bucket), Prefix: aws.String(prefix)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, object := range page.Contents {
			keys = append(keys, aws.ToString(object.Key))
		}
	}
	return keys, nil
}

func rustFSNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "NoSuchBucket", "NoSuchKey", "NotFound":
			return true
		}
	}
	var responseError *smithyhttp.ResponseError
	return errors.As(err, &responseError) && responseError.HTTPStatusCode() == 404
}

func digestMetadata(metadata map[string]string) string {
	for key, value := range metadata {
		if strings.EqualFold(key, "Artifact-Gateway-Sha256") || strings.EqualFold(key, "X-Amz-Meta-Artifact-Gateway-Sha256") {
			return value
		}
	}
	return ""
}
