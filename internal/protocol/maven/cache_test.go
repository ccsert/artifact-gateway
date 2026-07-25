package maven

import (
	"context"
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
