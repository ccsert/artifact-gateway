//go:build integration

package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestS3StoreContractAgainstConfiguredImplementation(t *testing.T) {
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	accessKey := os.Getenv("TEST_S3_ACCESS_KEY")
	secretKey := os.Getenv("TEST_S3_SECRET_KEY")
	if endpoint == "" || accessKey == "" || secretKey == "" {
		t.Skip("TEST_S3_ENDPOINT and credentials are required")
	}

	ctx := context.Background()
	store, err := NewS3Store(endpoint, accessKey, secretKey, "gateway-contract-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.EnsureBucket(ctx); err != nil {
		t.Fatal(err)
	}

	body := []byte("RustFS S3 contract: verified bytes, metadata, range, list, copy, delete")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key := "contract/artifact.bin"
	if err = store.PutVerifiedReader(ctx, key, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	info, err := store.Stat(ctx, key)
	if err != nil || info.Size != int64(len(body)) || info.Digest != digest {
		t.Fatalf("info=%#v err=%v", info, err)
	}
	reader, size, err := store.OpenRange(ctx, key, 7, 11)
	if err != nil {
		t.Fatal(err)
	}
	rangeBody, readErr := io.ReadAll(reader)
	_ = reader.Close()
	if readErr != nil || size != int64(len(body)) || string(rangeBody) != "S3 contract" {
		t.Fatalf("range=%q size=%d err=%v", rangeBody, size, readErr)
	}

	plainKey := "contract/plain.bin"
	if err = store.PutReader(ctx, plainKey, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	if err = store.SetVerifiedDigest(ctx, plainKey, digest); err != nil {
		t.Fatal(err)
	}
	if info, err = store.Stat(ctx, plainKey); err != nil || info.Digest != digest {
		t.Fatalf("copied metadata=%#v err=%v", info, err)
	}

	keys, err := store.List(ctx, "contract/")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{key, plainKey}) {
		t.Fatalf("keys=%#v", keys)
	}
	if err = store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted object err=%v", err)
	}
}
