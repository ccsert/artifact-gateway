package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestCacheQuotaRejectsNewEntryButAllowsReplacement(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	quota := NewCacheQuota(store, map[string]int64{"team/app": 5})
	cache := NewDefaultOCICache(store, nil).WithQuota(quota)
	key := cache.key("team", "team/app", ociManifest, "latest")
	first := []byte("12345")
	if err := cache.Store(context.Background(), key, CachedOCIContent{Body: first, Digest: digestOf(first), Repository: "team/app"}); err != nil {
		t.Fatal(err)
	}
	secondKey := cache.key("team", "team/app", ociManifest, "next")
	if err := cache.Store(context.Background(), secondKey, CachedOCIContent{Body: []byte("x"), Digest: digestOf([]byte("x")), Repository: "team/app"}); !errors.Is(err, ErrCacheQuotaExceeded) {
		t.Fatalf("new entry error = %v, want quota error", err)
	}
	replacement := []byte("1234")
	if err := cache.Store(context.Background(), key, CachedOCIContent{Body: replacement, Digest: digestOf(replacement), Repository: "team/app"}); err != nil {
		t.Fatalf("replacement error = %v", err)
	}
}

func TestCacheQuotaIgnoresExpiredIndexes(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	encoded, err := json.Marshal(cacheQuotaIndex{Repository: "engineering", Size: 10, ExpiresAt: time.Now().UTC().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "maven/index/expired.json", encoded); err != nil {
		t.Fatal(err)
	}
	quota := NewCacheQuota(store, map[string]int64{"engineering": 5})
	called := false
	if err := quota.Admit(context.Background(), "engineering", "maven/index/new.json", 5, func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("admission err=%v called=%t", err, called)
	}
}
