package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestCacheStoresContentAndIsolatesKeys(t *testing.T) {
	objects := objectstore.NewMemoryStore()
	cache := NewCache(objects, time.Hour, time.Minute, time.Minute, nil)
	first := cache.Key("team-a", "team-a/widget", "manifests", "latest")
	second := cache.Key("team-b", "team-b/widget", "manifests", "latest")
	if first == second {
		t.Fatal("cache keys must include the group and repository")
	}
	body := []byte(`{"schemaVersion":2}`)
	digest := sha256.Sum256(body)
	if err := cache.Store(context.Background(), first, CachedContent{Body: body, Digest: "sha256:" + hex.EncodeToString(digest[:]), Repository: "team-a/widget"}); err != nil {
		t.Fatal(err)
	}
	content, err := cache.Load(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	reader, size, err := content.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	actual, err := io.ReadAll(reader)
	if err != nil || size != int64(len(body)) || string(actual) != string(body) {
		t.Fatalf("content=%q size=%d err=%v", actual, size, err)
	}
}

func TestCacheNegativeEntryAndAllowlistUpdate(t *testing.T) {
	cache := NewDefaultCache(objectstore.NewMemoryStore(), []string{"registry.example"})
	if !cache.ProxyAllowed("https://registry.example/v2") {
		t.Fatal("configured proxy host was rejected")
	}
	cache.SetAllowedProxyHosts(nil)
	if cache.ProxyAllowed("https://registry.example/v2") {
		t.Fatal("removed proxy host remained allowed")
	}
	key := cache.Key("team", "team/widget", "manifests", "missing")
	if err := cache.StoreNegative(context.Background(), key, repository.Member{Name: "proxy", Endpoint: "https://registry.example"}); err != nil {
		t.Fatal(err)
	}
	content, err := cache.Load(context.Background(), key)
	if !errors.Is(err, ErrNegativeCache) || content.Member != "proxy" {
		t.Fatalf("content=%+v err=%v", content, err)
	}
}
