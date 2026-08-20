package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

type nonSeekReader struct{ io.Reader }

func TestMemoryStoreCopiesValuesAndSupportsRanges(t *testing.T) {
	store := NewMemoryStore()
	value := []byte("abcdef")
	if err := store.Put(context.Background(), "objects/a", value); err != nil {
		t.Fatal(err)
	}
	value[0] = 'x'

	reader, size, err := store.OpenRange(context.Background(), "objects/a", 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil || size != 6 || string(body) != "cde" {
		t.Fatalf("size=%d body=%q err=%v", size, body, err)
	}
}

func TestMemoryStoreReturnsStableNotFoundError(t *testing.T) {
	_, err := NewMemoryStore().Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("error=%v", err)
	}
}

func TestSeekableObjectReaderSpoolsNonSeekableInputAndValidatesSize(t *testing.T) {
	body := []byte("streamed object body")
	reader, cleanup, err := seekableObjectReader(nonSeekReader{Reader: bytes.NewReader(body)}, int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if spool, ok := reader.(*os.File); ok {
		if _, statErr := os.Stat(spool.Name()); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("spool path remained visible before cleanup: %v", statErr)
		}
	}
	got, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("spooled body=%q err=%v", got, err)
	}
	if _, _, err = seekableObjectReader(nonSeekReader{Reader: bytes.NewReader(body)}, int64(len(body)-1)); err == nil {
		t.Fatal("oversized non-seekable object reader was accepted")
	}
}
