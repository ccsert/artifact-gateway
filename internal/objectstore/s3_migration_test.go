package objectstore

import (
	"bytes"
	"context"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
)

type fakeBucketMigrationValue struct {
	object BucketMigrationObject
	body   []byte
}

type fakeBucketMigrationEndpoint struct {
	objects       map[string]fakeBucketMigrationValue
	bucketExists  bool
	ensureInvoked bool
}

func newFakeBucketMigrationEndpoint(values ...fakeBucketMigrationValue) *fakeBucketMigrationEndpoint {
	endpoint := &fakeBucketMigrationEndpoint{objects: make(map[string]fakeBucketMigrationValue), bucketExists: true}
	for _, value := range values {
		value.object.Size = int64(len(value.body))
		value.object.UserMetadata = maps.Clone(value.object.UserMetadata)
		endpoint.objects[value.object.Key] = value
	}
	return endpoint
}

func (e *fakeBucketMigrationEndpoint) CheckBucket(context.Context) error {
	if !e.bucketExists {
		return ErrMigrationBucketMissing
	}
	return nil
}

func (e *fakeBucketMigrationEndpoint) EnsureBucket(context.Context) error {
	e.ensureInvoked = true
	e.bucketExists = true
	return nil
}

func (e *fakeBucketMigrationEndpoint) ListBucketObjects(context.Context) ([]BucketMigrationObject, error) {
	objects := make([]BucketMigrationObject, 0, len(e.objects))
	for _, value := range e.objects {
		object := value.object
		object.UserMetadata = maps.Clone(object.UserMetadata)
		objects = append(objects, object)
	}
	return objects, nil
}

func (e *fakeBucketMigrationEndpoint) OpenBucketObject(_ context.Context, key string) (io.ReadCloser, error) {
	value, ok := e.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(value.body)), nil
}

func (e *fakeBucketMigrationEndpoint) PutBucketObject(_ context.Context, object BucketMigrationObject, body io.Reader) error {
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	object.UserMetadata = maps.Clone(object.UserMetadata)
	e.objects[object.Key] = fakeBucketMigrationValue{object: object, body: value}
	return nil
}

func (e *fakeBucketMigrationEndpoint) DeleteBucketObject(_ context.Context, key string) error {
	delete(e.objects, key)
	return nil
}

func TestCopyBucketPreservesBytesAndMetadataThenVerifiesExactInventory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := newFakeBucketMigrationEndpoint(
		fakeBucketMigrationValue{
			object: BucketMigrationObject{
				Key:                "raw/releases/app.bin",
				ContentType:        "application/octet-stream",
				ContentEncoding:    "identity",
				ContentDisposition: `attachment; filename="app.bin"`,
				ContentLanguage:    "zh-CN",
				CacheControl:       "public, max-age=60",
				UserMetadata: map[string]string{
					"Artifact-Gateway-Sha256": "sha256:abc",
					"Build":                   "42",
				},
			},
			body: []byte("immutable-release-bytes"),
		},
		fakeBucketMigrationValue{
			object: BucketMigrationObject{Key: "raw/releases/empty", ContentType: "application/octet-stream"},
			body:   []byte{},
		},
	)
	target := newFakeBucketMigrationEndpoint()
	target.bucketExists = false

	report, err := CopyBucket(ctx, source, target)
	if err != nil {
		t.Fatalf("copy bucket: %v", err)
	}
	if !target.ensureInvoked {
		t.Fatal("target bucket should be ensured before copying")
	}
	if report.Objects != 2 || report.Bytes != int64(len("immutable-release-bytes")) {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.ManifestSHA256) != 71 || !strings.HasPrefix(report.ManifestSHA256, "sha256:") {
		t.Fatalf("manifest digest should be a prefixed sha256: %q", report.ManifestSHA256)
	}

	got := target.objects["raw/releases/app.bin"]
	want := source.objects["raw/releases/app.bin"]
	if !bytes.Equal(got.body, want.body) {
		t.Fatalf("copied bytes = %q, want %q", got.body, want.body)
	}
	if got.object.ContentType != want.object.ContentType ||
		got.object.ContentEncoding != want.object.ContentEncoding ||
		got.object.ContentDisposition != want.object.ContentDisposition ||
		got.object.ContentLanguage != want.object.ContentLanguage ||
		got.object.CacheControl != want.object.CacheControl ||
		!maps.Equal(got.object.UserMetadata, want.object.UserMetadata) {
		t.Fatalf("copied metadata = %#v, want %#v", got.object, want.object)
	}

	verified, err := VerifyBucket(ctx, source, target)
	if err != nil {
		t.Fatalf("verify bucket: %v", err)
	}
	if verified != report {
		t.Fatalf("verified report = %+v, copied report = %+v", verified, report)
	}
}

func TestVerifyBucketRejectsAnyInventoryContentOrMetadataDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := fakeBucketMigrationValue{
		object: BucketMigrationObject{
			Key:          "objects/one",
			ContentType:  "text/plain",
			UserMetadata: map[string]string{"Release": "stable"},
		},
		body: []byte("one"),
	}

	tests := []struct {
		name   string
		mutate func(*fakeBucketMigrationEndpoint)
	}{
		{
			name: "extra target key",
			mutate: func(target *fakeBucketMigrationEndpoint) {
				target.objects["objects/extra"] = fakeBucketMigrationValue{
					object: BucketMigrationObject{Key: "objects/extra", Size: 5},
					body:   []byte("extra"),
				}
			},
		},
		{
			name: "different body",
			mutate: func(target *fakeBucketMigrationEndpoint) {
				value := target.objects["objects/one"]
				value.body = []byte("two")
				target.objects["objects/one"] = value
			},
		},
		{
			name: "different user metadata",
			mutate: func(target *fakeBucketMigrationEndpoint) {
				value := target.objects["objects/one"]
				value.object.UserMetadata = map[string]string{"Release": "candidate"}
				target.objects["objects/one"] = value
			},
		},
		{
			name: "different content type",
			mutate: func(target *fakeBucketMigrationEndpoint) {
				value := target.objects["objects/one"]
				value.object.ContentType = "application/octet-stream"
				target.objects["objects/one"] = value
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := newFakeBucketMigrationEndpoint(base)
			target := newFakeBucketMigrationEndpoint(base)
			tt.mutate(target)

			_, err := VerifyBucket(ctx, source, target)
			if err == nil {
				t.Fatal("verification should reject drift")
			}
			if !strings.Contains(err.Error(), "bucket verification failed") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestMirrorBucketRemovesOnlyTargetExtrasAndVerifiesExactInventory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	source := newFakeBucketMigrationEndpoint(fakeBucketMigrationValue{
		object: BucketMigrationObject{Key: "objects/current", UserMetadata: map[string]string{"release": "42"}},
		body:   []byte("current"),
	})
	target := newFakeBucketMigrationEndpoint(
		fakeBucketMigrationValue{object: BucketMigrationObject{Key: "objects/current"}, body: []byte("old")},
		fakeBucketMigrationValue{object: BucketMigrationObject{Key: "objects/stale"}, body: []byte("stale")},
	)

	report, err := MirrorBucket(ctx, source, target)
	if err != nil {
		t.Fatalf("mirror bucket: %v", err)
	}
	if report.Objects != 1 || report.Bytes != int64(len("current")) {
		t.Fatalf("unexpected report: %+v", report)
	}
	if _, exists := target.objects["objects/stale"]; exists {
		t.Fatal("target-only object survived exact mirror")
	}
	if !bytes.Equal(target.objects["objects/current"].body, []byte("current")) {
		t.Fatalf("current object = %q", target.objects["objects/current"].body)
	}
}

func TestBucketManifestIsStableAcrossListAndMetadataOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	values := []fakeBucketMigrationValue{
		{object: BucketMigrationObject{Key: "z", UserMetadata: map[string]string{"B": "2", "A": "1"}}, body: []byte("z")},
		{object: BucketMigrationObject{Key: "a"}, body: []byte("a")},
	}
	source := newFakeBucketMigrationEndpoint(values...)
	target := newFakeBucketMigrationEndpoint(slices.Clone(values)...)
	targetValue := target.objects["z"]
	targetValue.object.UserMetadata = map[string]string{"a": "1", "b": "2"}
	target.objects["z"] = targetValue

	report, err := VerifyBucket(ctx, source, target)
	if err != nil {
		t.Fatalf("metadata names should compare case-insensitively: %v", err)
	}
	if report.Objects != 2 {
		t.Fatalf("objects = %d, want 2", report.Objects)
	}
}
