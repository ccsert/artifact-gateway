package maven

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestCacheStoresComponentsAndExpiresMetadataSeparately(t *testing.T) {
	cache := NewCache(objectstore.NewMemoryStore(), time.Hour, -time.Millisecond, time.Minute, time.Minute, nil)
	componentKey := cache.Key("engineering", "com/example/widget/1.0/widget-1.0.jar")
	if err := cache.Store(context.Background(), componentKey, "com/example/widget/1.0/widget-1.0.jar", CachedContent{Body: []byte("jar"), Repository: "engineering"}); err != nil {
		t.Fatal(err)
	}
	component, err := cache.Load(context.Background(), componentKey)
	if err != nil || string(component.Body) != "jar" {
		t.Fatalf("component=%q err=%v", component.Body, err)
	}
	metadataKey := cache.Key("engineering", "com/example/widget/maven-metadata.xml")
	if err := cache.Store(context.Background(), metadataKey, "com/example/widget/maven-metadata.xml", CachedContent{Body: []byte("metadata"), Repository: "engineering"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Load(context.Background(), metadataKey); !errors.Is(err, ErrCacheMiss) {
		t.Fatalf("metadata error=%v, want cache miss", err)
	}
}

func TestCacheNegativeEntryAndAllowlistUpdate(t *testing.T) {
	cache := NewDefaultCache(objectstore.NewMemoryStore(), []string{"repo.example"})
	if !cache.ProxyAllowed("https://repo.example/maven") {
		t.Fatal("configured proxy host was rejected")
	}
	cache.SetAllowedProxyHosts(nil)
	if cache.ProxyAllowed("https://repo.example/maven") {
		t.Fatal("removed proxy host remained allowed")
	}
	key := cache.Key("engineering", "com/example/missing/1.0/missing.pom")
	if err := cache.StoreNegative(context.Background(), key, repository.Member{Name: "central", Endpoint: "https://repo.example"}); err != nil {
		t.Fatal(err)
	}
	content, err := cache.Load(context.Background(), key)
	if !errors.Is(err, ErrNegativeCache) || content.Member != "central" {
		t.Fatalf("content=%+v err=%v", content, err)
	}
}

func TestCacheWithTTLsAssignsIndependentLifetimes(t *testing.T) {
	store := objectstore.NewMemoryStore()
	cache := NewDefaultCache(store, nil).WithTTLs(time.Hour, 10*time.Minute, 5*time.Minute)
	ctx := context.Background()
	componentKey := cache.Key("engineering", "com/example/widget/1.0/widget-1.0.jar")
	metadataKey := cache.Key("engineering", "com/example/widget/maven-metadata.xml")
	negativeKey := cache.Key("engineering", "com/example/missing/maven-metadata.xml")
	if err := cache.Store(ctx, componentKey, "com/example/widget/1.0/widget-1.0.jar", CachedContent{Body: []byte("jar"), Repository: "engineering"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Store(ctx, metadataKey, "com/example/widget/maven-metadata.xml", CachedContent{Body: []byte("metadata"), Repository: "engineering"}); err != nil {
		t.Fatal(err)
	}
	if err := cache.StoreNegative(ctx, negativeKey, repository.Member{Name: "central", Endpoint: "https://repo.example"}); err != nil {
		t.Fatal(err)
	}
	readIndex := func(key string) cachedMavenIndex {
		t.Helper()
		encoded, err := store.Get(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		var index cachedMavenIndex
		if err := json.Unmarshal(encoded, &index); err != nil {
			t.Fatal(err)
		}
		return index
	}
	component := readIndex(componentKey)
	metadata := readIndex(metadataKey)
	negative := readIndex(negativeKey)
	if component.ExpiresAt.Sub(metadata.ExpiresAt) < 49*time.Minute || metadata.ExpiresAt.Sub(negative.ExpiresAt) < 4*time.Minute {
		t.Fatalf("expiry order = component:%v metadata:%v negative:%v", component.ExpiresAt, metadata.ExpiresAt, negative.ExpiresAt)
	}
}
