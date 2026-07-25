package objectstore

import (
	"context"
	"errors"
	"io"
	"testing"
)

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
