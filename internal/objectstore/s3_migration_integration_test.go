//go:build integration

package objectstore

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestS3MigrationCopiesMinIOToRustFSAndSupportsExactRollback(t *testing.T) {
	ctx := context.Background()
	minioEndpoint := os.Getenv("TEST_MIGRATION_MINIO_ENDPOINT")
	rustfsEndpoint := os.Getenv("TEST_MIGRATION_RUSTFS_ENDPOINT")
	if minioEndpoint == "" || rustfsEndpoint == "" {
		t.Skip("TEST_MIGRATION_MINIO_ENDPOINT and TEST_MIGRATION_RUSTFS_ENDPOINT are required")
	}
	minioBucket := "migration-minio-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	rustfsBucket := "migration-rustfs-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	minioStore, err := NewS3BucketMigrationEndpoint(
		minioEndpoint,
		os.Getenv("TEST_MIGRATION_MINIO_ACCESS_KEY"),
		os.Getenv("TEST_MIGRATION_MINIO_SECRET_KEY"),
		minioBucket,
	)
	if err != nil {
		t.Fatal(err)
	}
	rustfsStore, err := NewS3BucketMigrationEndpoint(
		rustfsEndpoint,
		os.Getenv("TEST_MIGRATION_RUSTFS_ACCESS_KEY"),
		os.Getenv("TEST_MIGRATION_RUSTFS_SECRET_KEY"),
		rustfsBucket,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err = minioStore.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}
	if err = rustfsStore.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}

	original := BucketMigrationObject{
		Key:                "raw/releases/app.bin",
		Size:               int64(len("immutable-release")),
		ContentType:        "application/octet-stream",
		ContentEncoding:    "identity",
		ContentDisposition: `attachment; filename="app.bin"`,
		ContentLanguage:    "zh-CN",
		CacheControl:       "public, max-age=60",
		UserMetadata: map[string]string{
			"artifact-gateway-sha256": "sha256:" + strings.Repeat("a", 64),
			"build":                   "42",
		},
	}
	if err = minioStore.PutBucketObject(ctx, original, bytes.NewBufferString("immutable-release")); err != nil {
		t.Fatal(err)
	}
	report, err := CopyBucket(ctx, minioStore, rustfsStore)
	if err != nil {
		t.Fatalf("copy MinIO to RustFS: %v", err)
	}
	if report.Objects != 1 || report.Bytes != int64(len("immutable-release")) {
		t.Fatalf("unexpected cutover report: %+v", report)
	}

	// Simulate a post-cutover deletion and new write on RustFS. Exact rollback
	// must remove the deleted key from frozen MinIO and preserve the new key.
	if err = rustfsStore.DeleteBucketObject(ctx, original.Key); err != nil {
		t.Fatal(err)
	}
	postCutover := BucketMigrationObject{
		Key:          "raw/releases/app-v2.bin",
		Size:         int64(len("post-cutover")),
		ContentType:  "application/octet-stream",
		UserMetadata: map[string]string{"artifact-gateway-sha256": "sha256:" + strings.Repeat("b", 64)},
	}
	if err = rustfsStore.PutBucketObject(ctx, postCutover, bytes.NewBufferString("post-cutover")); err != nil {
		t.Fatal(err)
	}
	rollback, err := MirrorBucket(ctx, rustfsStore, minioStore)
	if err != nil {
		t.Fatalf("mirror RustFS back to MinIO: %v", err)
	}
	if rollback.Objects != 1 || rollback.Bytes != int64(len("post-cutover")) {
		t.Fatalf("unexpected rollback report: %+v", rollback)
	}
}
