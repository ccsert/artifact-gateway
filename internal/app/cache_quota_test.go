package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
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
	encoded, err := json.Marshal(struct {
		Repository string    `json:"repository"`
		Size       int64     `json:"size"`
		ExpiresAt  time.Time `json:"expires_at"`
	}{Repository: "engineering", Size: 10, ExpiresAt: time.Now().UTC().Add(-time.Minute)})
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

func TestConanCacheQuotaAndMemberEndpointIsolation(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewDefaultConanCache(store, nil).WithQuota(NewCacheQuota(store, nil))
	first := repository.Member{Name: "hosted", Endpoint: "https://one.example"}
	second := repository.Member{Name: "hosted", Endpoint: "https://two.example"}
	if err := cache.store(context.Background(), cache.key("central", "pkg/1", first), conanCacheEntry{body: []byte("12345"), member: first.Name, endpoint: first.Endpoint}, "central", 5, time.Hour, "central", "pkg/1", ""); err != nil {
		t.Fatal(err)
	}
	if err := cache.store(context.Background(), cache.key("central", "pkg/1", second), conanCacheEntry{body: []byte("x"), member: second.Name, endpoint: second.Endpoint}, "central", 5, time.Hour); !errors.Is(err, ErrCacheQuotaExceeded) {
		t.Fatalf("quota error=%v", err)
	}
	cache.Invalidate(context.Background(), "central", "pkg/1", first)
	if _, ok := cache.load(context.Background(), cache.key("central", "pkg/1", first)); ok {
		t.Fatal("first endpoint was not invalidated")
	}
}

func TestConanCacheCollectsExpiredIndexesAndObjects(t *testing.T) {
	store := NewMemoryOCIObjectStore()
	cache := NewDefaultConanCache(store, nil)
	member := repository.Member{Name: "hosted", Endpoint: "https://hosted.example"}
	if err := cache.store(context.Background(), cache.key("central", "pkg/1", member), conanCacheEntry{body: []byte("expired"), member: member.Name, endpoint: member.Endpoint}, "central", 1024, -time.Second); err != nil {
		t.Fatal(err)
	}
	if err := cache.CollectGarbage(context.Background()); err != nil {
		t.Fatal(err)
	}
	objects, err := store.List(context.Background(), "conan/objects/")
	if err != nil || len(objects) != 0 {
		t.Fatalf("objects=%v err=%v", objects, err)
	}
}
