package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
)

type cliMigrationEndpoint struct {
	objects map[string][]byte
	ensured bool
}

func (e *cliMigrationEndpoint) CheckBucket(context.Context) error { return nil }

func (e *cliMigrationEndpoint) EnsureBucket(context.Context) error {
	e.ensured = true
	return nil
}

func (e *cliMigrationEndpoint) ListBucketObjects(context.Context) ([]objectstore.BucketMigrationObject, error) {
	objects := make([]objectstore.BucketMigrationObject, 0, len(e.objects))
	for key, value := range e.objects {
		objects = append(objects, objectstore.BucketMigrationObject{Key: key, Size: int64(len(value)), ContentType: "application/octet-stream"})
	}
	return objects, nil
}

func (e *cliMigrationEndpoint) OpenBucketObject(_ context.Context, key string) (io.ReadCloser, error) {
	value, ok := e.objects[key]
	if !ok {
		return nil, objectstore.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(value)), nil
}

func (e *cliMigrationEndpoint) PutBucketObject(_ context.Context, object objectstore.BucketMigrationObject, body io.Reader) error {
	value, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	e.objects[object.Key] = value
	return nil
}

func (e *cliMigrationEndpoint) DeleteBucketObject(_ context.Context, key string) error {
	delete(e.objects, key)
	return nil
}

func TestRunRequiresCredentialsFromEnvironmentAndNeverAcceptsSecretFlags(t *testing.T) {
	t.Parallel()
	args := []string{
		"copy",
		"--source-endpoint", "http://minio:9000",
		"--source-bucket", "gateway-cache",
		"--target-endpoint", "http://rustfs:9000",
		"--target-bucket", "gateway-cache",
	}

	err := run(context.Background(), args, func(string) string { return "" }, io.Discard, io.Discard, nil)
	if err == nil || err.Error() != "S3_MIGRATION_SOURCE_ACCESS_KEY, S3_MIGRATION_SOURCE_SECRET_KEY, S3_MIGRATION_TARGET_ACCESS_KEY and S3_MIGRATION_TARGET_SECRET_KEY are required" {
		t.Fatalf("missing credential error = %v", err)
	}

	withSecretFlag := append(args, "--source-secret-key", "must-not-be-a-flag")
	err = run(context.Background(), withSecretFlag, func(string) string { return "present" }, io.Discard, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -source-secret-key") {
		t.Fatalf("secret flag should be rejected, got %v", err)
	}
}

func TestRunCopyEmitsVerifiedJSONEvidence(t *testing.T) {
	t.Parallel()
	source := &cliMigrationEndpoint{objects: map[string][]byte{"raw/app.bin": []byte("release")}}
	target := &cliMigrationEndpoint{objects: map[string][]byte{}}
	factory := func(config endpointConfig) (objectstore.BucketMigrationEndpoint, error) {
		switch config.endpoint {
		case "http://minio:9000":
			return source, nil
		case "http://rustfs:9000":
			return target, nil
		default:
			return nil, errors.New("unexpected endpoint")
		}
	}
	getenv := func(string) string { return "credential" }
	var stdout bytes.Buffer

	err := run(context.Background(), []string{
		"copy",
		"--source-endpoint", "http://minio:9000",
		"--source-bucket", "gateway-cache",
		"--target-endpoint", "http://rustfs:9000",
		"--target-bucket", "gateway-cache",
	}, getenv, &stdout, io.Discard, factory)
	if err != nil {
		t.Fatalf("run copy: %v", err)
	}
	if !target.ensured {
		t.Fatal("copy should ensure the target bucket")
	}
	if got := stdout.String(); !strings.Contains(got, `"operation":"copy"`) ||
		!strings.Contains(got, `"objects":1`) ||
		!strings.Contains(got, `"bytes":7`) ||
		!strings.Contains(got, `"manifestSha256":"sha256:`) {
		t.Fatalf("unexpected JSON evidence: %s", got)
	}
}

func TestRunHasStablePublicUsageErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args []string
		want string
	}{
		{args: nil, want: "usage: s3-migrate <inventory|copy|mirror|verify> [options]"},
		{args: []string{"delete"}, want: `unknown operation "delete"`},
		{args: []string{"inventory"}, want: "--source-endpoint and --source-bucket are required"},
		{args: []string{"mirror", "--source-endpoint", "http://minio:9000", "--source-bucket", "source", "--target-endpoint", "http://rustfs:9000", "--target-bucket", "target"}, want: "mirror requires --delete-target-extras and both write paths must be frozen"},
	}
	for _, tt := range tests {
		err := run(context.Background(), tt.args, func(string) string { return "credential" }, io.Discard, io.Discard, nil)
		if err == nil || err.Error() != tt.want {
			t.Fatalf("run(%v) error = %v, want %q", tt.args, err, tt.want)
		}
	}
}
