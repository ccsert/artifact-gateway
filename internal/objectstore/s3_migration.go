package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrMigrationBucketMissing indicates that a source or verification bucket is
// not present. CopyBucket creates the target bucket, but never the source.
var ErrMigrationBucketMissing = errors.New("migration bucket does not exist")

// BucketMigrationObject contains the durable HTTP and user metadata that must
// survive an S3-compatible object-store migration.
type BucketMigrationObject struct {
	Key                string
	Size               int64
	ContentType        string
	ContentEncoding    string
	ContentDisposition string
	ContentLanguage    string
	CacheControl       string
	UserMetadata       map[string]string
}

// BucketMigrationEndpoint is deliberately streaming: migrations must not load
// an artifact into memory, regardless of its size.
type BucketMigrationEndpoint interface {
	CheckBucket(context.Context) error
	EnsureBucket(context.Context) error
	ListBucketObjects(context.Context) ([]BucketMigrationObject, error)
	OpenBucketObject(context.Context, string) (io.ReadCloser, error)
	PutBucketObject(context.Context, BucketMigrationObject, io.Reader) error
	DeleteBucketObject(context.Context, string) error
}

// BucketMigrationReport is both operator evidence and an exact inventory
// fingerprint. ManifestSHA256 covers sorted keys, bytes, content metadata and
// normalized user metadata.
type BucketMigrationReport struct {
	Objects        int    `json:"objects"`
	Bytes          int64  `json:"bytes"`
	ManifestSHA256 string `json:"manifestSha256"`
}

type bucketMigrationRecord struct {
	Key                string            `json:"key"`
	Size               int64             `json:"size"`
	SHA256             string            `json:"sha256"`
	ContentType        string            `json:"contentType,omitempty"`
	ContentEncoding    string            `json:"contentEncoding,omitempty"`
	ContentDisposition string            `json:"contentDisposition,omitempty"`
	ContentLanguage    string            `json:"contentLanguage,omitempty"`
	CacheControl       string            `json:"cacheControl,omitempty"`
	UserMetadata       map[string]string `json:"userMetadata,omitempty"`
}

// InventoryBucket produces the same byte-level manifest used by verification
// without requiring a target endpoint.
func InventoryBucket(ctx context.Context, endpoint BucketMigrationEndpoint) (BucketMigrationReport, error) {
	_, report, err := snapshotBucket(ctx, endpoint)
	if err != nil {
		return BucketMigrationReport{}, err
	}
	return report, nil
}

// CopyBucket streams every source object to the target and then performs a
// full, byte-level verification. It never deletes target objects, so an extra
// target key makes verification fail instead of being silently removed.
func CopyBucket(ctx context.Context, source, target BucketMigrationEndpoint) (BucketMigrationReport, error) {
	return copyBucket(ctx, source, target, false)
}

// MirrorBucket is the explicit reconciliation operation used only while both
// write paths are frozen. Unlike CopyBucket, it removes target-only keys before
// verification so a rollback can restore an exact source inventory.
func MirrorBucket(ctx context.Context, source, target BucketMigrationEndpoint) (BucketMigrationReport, error) {
	return copyBucket(ctx, source, target, true)
}

func copyBucket(ctx context.Context, source, target BucketMigrationEndpoint, deleteTargetExtras bool) (BucketMigrationReport, error) {
	if err := source.CheckBucket(ctx); err != nil {
		return BucketMigrationReport{}, fmt.Errorf("check source bucket: %w", err)
	}
	if err := target.EnsureBucket(ctx); err != nil {
		return BucketMigrationReport{}, fmt.Errorf("ensure target bucket: %w", err)
	}
	objects, err := listMigrationObjects(ctx, source)
	if err != nil {
		return BucketMigrationReport{}, fmt.Errorf("list source bucket: %w", err)
	}
	for _, object := range objects {
		body, err := source.OpenBucketObject(ctx, object.Key)
		if err != nil {
			return BucketMigrationReport{}, fmt.Errorf("open source object %q: %w", object.Key, err)
		}
		putErr := target.PutBucketObject(ctx, cloneMigrationObject(object), body)
		closeErr := body.Close()
		if putErr != nil {
			return BucketMigrationReport{}, fmt.Errorf("copy object %q: %w", object.Key, putErr)
		}
		if closeErr != nil {
			return BucketMigrationReport{}, fmt.Errorf("close source object %q: %w", object.Key, closeErr)
		}
	}
	if deleteTargetExtras {
		targetObjects, listErr := listMigrationObjects(ctx, target)
		if listErr != nil {
			return BucketMigrationReport{}, fmt.Errorf("list target bucket: %w", listErr)
		}
		sourceKeys := make(map[string]struct{}, len(objects))
		for _, object := range objects {
			sourceKeys[object.Key] = struct{}{}
		}
		for _, object := range targetObjects {
			if _, exists := sourceKeys[object.Key]; exists {
				continue
			}
			if err := target.DeleteBucketObject(ctx, object.Key); err != nil {
				return BucketMigrationReport{}, fmt.Errorf("delete target-only object %q: %w", object.Key, err)
			}
		}
	}
	return VerifyBucket(ctx, source, target)
}

// VerifyBucket compares exact source and target manifests. Each manifest is
// computed by streaming every object and hashing its bytes.
func VerifyBucket(ctx context.Context, source, target BucketMigrationEndpoint) (BucketMigrationReport, error) {
	sourceRecords, sourceReport, err := snapshotBucket(ctx, source)
	if err != nil {
		return BucketMigrationReport{}, fmt.Errorf("snapshot source bucket: %w", err)
	}
	targetRecords, targetReport, err := snapshotBucket(ctx, target)
	if err != nil {
		return BucketMigrationReport{}, fmt.Errorf("snapshot target bucket: %w", err)
	}
	sourceManifest, err := json.Marshal(sourceRecords)
	if err != nil {
		return BucketMigrationReport{}, fmt.Errorf("encode source manifest: %w", err)
	}
	targetManifest, err := json.Marshal(targetRecords)
	if err != nil {
		return BucketMigrationReport{}, fmt.Errorf("encode target manifest: %w", err)
	}
	if !bytes.Equal(sourceManifest, targetManifest) {
		return BucketMigrationReport{}, fmt.Errorf(
			"bucket verification failed: source objects=%d bytes=%d manifest=%s, target objects=%d bytes=%d manifest=%s",
			sourceReport.Objects,
			sourceReport.Bytes,
			sourceReport.ManifestSHA256,
			targetReport.Objects,
			targetReport.Bytes,
			targetReport.ManifestSHA256,
		)
	}
	return sourceReport, nil
}

func snapshotBucket(ctx context.Context, endpoint BucketMigrationEndpoint) ([]bucketMigrationRecord, BucketMigrationReport, error) {
	if err := endpoint.CheckBucket(ctx); err != nil {
		return nil, BucketMigrationReport{}, err
	}
	objects, err := listMigrationObjects(ctx, endpoint)
	if err != nil {
		return nil, BucketMigrationReport{}, err
	}
	records := make([]bucketMigrationRecord, 0, len(objects))
	var totalBytes int64
	for _, object := range objects {
		body, err := endpoint.OpenBucketObject(ctx, object.Key)
		if err != nil {
			return nil, BucketMigrationReport{}, fmt.Errorf("open object %q: %w", object.Key, err)
		}
		digest := sha256.New()
		read, readErr := io.Copy(digest, body)
		closeErr := body.Close()
		if readErr != nil {
			return nil, BucketMigrationReport{}, fmt.Errorf("hash object %q: %w", object.Key, readErr)
		}
		if closeErr != nil {
			return nil, BucketMigrationReport{}, fmt.Errorf("close object %q: %w", object.Key, closeErr)
		}
		if read != object.Size {
			return nil, BucketMigrationReport{}, fmt.Errorf("object %q size changed while reading: listed=%d read=%d", object.Key, object.Size, read)
		}
		records = append(records, bucketMigrationRecord{
			Key:                object.Key,
			Size:               object.Size,
			SHA256:             "sha256:" + hex.EncodeToString(digest.Sum(nil)),
			ContentType:        object.ContentType,
			ContentEncoding:    object.ContentEncoding,
			ContentDisposition: object.ContentDisposition,
			ContentLanguage:    object.ContentLanguage,
			CacheControl:       object.CacheControl,
			UserMetadata:       normalizeMigrationMetadata(object.UserMetadata),
		})
		totalBytes += read
	}
	manifest, err := json.Marshal(records)
	if err != nil {
		return nil, BucketMigrationReport{}, err
	}
	digest := sha256.Sum256(manifest)
	return records, BucketMigrationReport{
		Objects:        len(records),
		Bytes:          totalBytes,
		ManifestSHA256: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func listMigrationObjects(ctx context.Context, endpoint BucketMigrationEndpoint) ([]BucketMigrationObject, error) {
	objects, err := endpoint.ListBucketObjects(ctx)
	if err != nil {
		return nil, err
	}
	objects = slices.Clone(objects)
	slices.SortFunc(objects, func(a, b BucketMigrationObject) int { return strings.Compare(a.Key, b.Key) })
	for i := range objects {
		if objects[i].Key == "" {
			return nil, errors.New("migration object has an empty key")
		}
		if objects[i].Size < 0 {
			return nil, fmt.Errorf("migration object %q has a negative size", objects[i].Key)
		}
		if i > 0 && objects[i-1].Key == objects[i].Key {
			return nil, fmt.Errorf("migration object key %q is duplicated", objects[i].Key)
		}
	}
	return objects, nil
}

func cloneMigrationObject(object BucketMigrationObject) BucketMigrationObject {
	object.UserMetadata = maps.Clone(object.UserMetadata)
	return object
}

func normalizeMigrationMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(metadata))
	for key, value := range metadata {
		key = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(key)), "x-amz-meta-")
		normalized[key] = value
	}
	return normalized
}

// S3BucketMigrationEndpoint adapts any S3-compatible service, including MinIO
// and RustFS, to the streaming migration port.
type S3BucketMigrationEndpoint struct {
	client *minio.Client
	bucket string
}

func NewS3BucketMigrationEndpoint(endpoint, accessKey, secretKey, bucket string) (*S3BucketMigrationEndpoint, error) {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(bucket) == "" {
		return nil, errors.New("S3 migration endpoint and bucket are required")
	}
	client, err := minio.New(strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://"), &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: strings.HasPrefix(endpoint, "https://"),
	})
	if err != nil {
		return nil, err
	}
	return &S3BucketMigrationEndpoint{client: client, bucket: bucket}, nil
}

func (e *S3BucketMigrationEndpoint) CheckBucket(ctx context.Context) error {
	exists, err := e.client.BucketExists(ctx, e.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return ErrMigrationBucketMissing
	}
	return nil
}

func (e *S3BucketMigrationEndpoint) EnsureBucket(ctx context.Context) error {
	exists, err := e.client.BucketExists(ctx, e.bucket)
	if err != nil || exists {
		return err
	}
	return e.client.MakeBucket(ctx, e.bucket, minio.MakeBucketOptions{})
}

func (e *S3BucketMigrationEndpoint) ListBucketObjects(ctx context.Context) ([]BucketMigrationObject, error) {
	objects := make([]BucketMigrationObject, 0)
	for listed := range e.client.ListObjects(ctx, e.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if listed.Err != nil {
			return nil, listed.Err
		}
		info, err := e.client.StatObject(ctx, e.bucket, listed.Key, minio.StatObjectOptions{})
		if err != nil {
			return nil, fmt.Errorf("stat object %q: %w", listed.Key, err)
		}
		objects = append(objects, BucketMigrationObject{
			Key:                info.Key,
			Size:               info.Size,
			ContentType:        firstNonEmpty(info.ContentType, info.Metadata.Get("Content-Type")),
			ContentEncoding:    info.Metadata.Get("Content-Encoding"),
			ContentDisposition: info.Metadata.Get("Content-Disposition"),
			ContentLanguage:    info.Metadata.Get("Content-Language"),
			CacheControl:       info.Metadata.Get("Cache-Control"),
			UserMetadata:       migrationUserMetadata(info),
		})
	}
	return objects, nil
}

func (e *S3BucketMigrationEndpoint) OpenBucketObject(ctx context.Context, key string) (io.ReadCloser, error) {
	return e.client.GetObject(ctx, e.bucket, key, minio.GetObjectOptions{})
}

func (e *S3BucketMigrationEndpoint) PutBucketObject(ctx context.Context, object BucketMigrationObject, body io.Reader) error {
	_, err := e.client.PutObject(ctx, e.bucket, object.Key, body, object.Size, minio.PutObjectOptions{
		ContentType:        object.ContentType,
		ContentEncoding:    object.ContentEncoding,
		ContentDisposition: object.ContentDisposition,
		ContentLanguage:    object.ContentLanguage,
		CacheControl:       object.CacheControl,
		UserMetadata:       maps.Clone(object.UserMetadata),
	})
	return err
}

func (e *S3BucketMigrationEndpoint) DeleteBucketObject(ctx context.Context, key string) error {
	return e.client.RemoveObject(ctx, e.bucket, key, minio.RemoveObjectOptions{})
}

func migrationUserMetadata(info minio.ObjectInfo) map[string]string {
	metadata := make(map[string]string, len(info.UserMetadata))
	for key, value := range info.UserMetadata {
		metadata[key] = value
	}
	for key, values := range info.Metadata {
		if !strings.HasPrefix(strings.ToLower(key), "x-amz-meta-") || len(values) == 0 {
			continue
		}
		metadata[strings.TrimPrefix(strings.ToLower(key), "x-amz-meta-")] = values[0]
	}
	return normalizeMigrationMetadata(metadata)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

var _ BucketMigrationEndpoint = (*S3BucketMigrationEndpoint)(nil)
