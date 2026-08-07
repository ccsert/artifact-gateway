package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestUploadSpoolComputesDigestAndRewinds(t *testing.T) {
	body := strings.Repeat("streamed-upload-", 1<<16)
	spool, err := spoolUpload(strings.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	name := spool.file.Name()
	defer func() {
		if err := spool.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary upload still exists: %v", err)
		}
	}()
	got, err := io.ReadAll(spool.Reader())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	wantDigest := "sha256:" + hex.EncodeToString(sum[:])
	if !bytes.Equal(got, []byte(body)) || spool.Size() != int64(len(body)) || spool.Digest() != wantDigest {
		t.Fatalf("spooled upload mismatch: size=%d digest=%q", spool.Size(), spool.Digest())
	}
}

func TestUploadSpoolRejectsOversizedBody(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	if spool, err := spoolUpload(strings.NewReader("12345"), 4); !errors.Is(err, errUploadTooLarge) || spool != nil {
		t.Fatalf("spool=%v err=%v", spool, err)
	}
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("oversized upload left temporary files: %v", entries)
	}
}

func TestUploadSpoolAppendsWithoutHoldingCombinedBody(t *testing.T) {
	spool, err := newUploadSpool()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	if n, err := spool.Append(strings.NewReader("first"), 5); err != nil || n != 5 {
		t.Fatalf("first append n=%d err=%v", n, err)
	}
	if n, err := spool.Append(strings.NewReader("second"), 6); err != nil || n != 6 {
		t.Fatalf("second append n=%d err=%v", n, err)
	}
	if err := spool.Rewind(); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(spool.Reader())
	if err != nil || string(got) != "firstsecond" {
		t.Fatalf("body=%q err=%v", got, err)
	}
}

func TestSpoolObjectAppendStreamsExistingAndNewBytes(t *testing.T) {
	objects := NewMemoryOCIObjectStore()
	if err := objects.Put(context.Background(), "upload", []byte("first")); err != nil {
		t.Fatal(err)
	}
	spool, chunkSize, err := spoolObjectAppend(context.Background(), objects, "upload", 5, strings.NewReader("second"), 6)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	body, err := io.ReadAll(spool.Reader())
	if err != nil || string(body) != "firstsecond" || chunkSize != 6 || spool.Size() != 11 {
		t.Fatalf("body=%q chunk=%d size=%d err=%v", body, chunkSize, spool.Size(), err)
	}
}

type observedOpenObjectStore struct {
	OCIObjectStore
	opens      int
	sizeOffset int64
}

func (s *observedOpenObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	s.opens++
	reader, size, err := s.OCIObjectStore.Open(ctx, key)
	return reader, size + s.sizeOffset, err
}

func TestSpoolStoredObjectReadsOnceAndReusesDigest(t *testing.T) {
	base := NewMemoryOCIObjectStore()
	if err := base.Put(context.Background(), "upload", []byte("content")); err != nil {
		t.Fatal(err)
	}
	objects := &observedOpenObjectStore{OCIObjectStore: base}
	spool, err := spoolStoredObject(context.Background(), objects, "upload")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = spool.Close() }()
	want := sha256.Sum256([]byte("content"))
	if objects.opens != 1 || spool.Digest() != "sha256:"+hex.EncodeToString(want[:]) || !bytes.Equal(spool.DigestBytes(), want[:]) || spool.Size() != 7 {
		t.Fatalf("opens=%d digest=%q sum=%x size=%d", objects.opens, spool.Digest(), spool.DigestBytes(), spool.Size())
	}
}

func TestSpoolStoredObjectRejectsChangedSize(t *testing.T) {
	base := NewMemoryOCIObjectStore()
	if err := base.Put(context.Background(), "upload", []byte("content")); err != nil {
		t.Fatal(err)
	}
	objects := &observedOpenObjectStore{OCIObjectStore: base, sizeOffset: 1}
	if spool, err := spoolStoredObject(context.Background(), objects, "upload"); err == nil || spool != nil {
		t.Fatalf("spool=%v err=%v", spool, err)
	}
}
