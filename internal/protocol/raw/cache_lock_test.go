package raw

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
)

type rawRenewalCoordinator struct {
	mu       sync.Mutex
	renewals int
	fail     bool
}

func (*rawRenewalCoordinator) Acquire(context.Context, string, time.Duration) (string, bool, error) {
	return "owner", true, nil
}

func (c *rawRenewalCoordinator) Renew(context.Context, string, string, time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.renewals++
	return !c.fail, nil
}

func (*rawRenewalCoordinator) Release(context.Context, string, string) error { return nil }
func (*rawRenewalCoordinator) CircuitOpen(context.Context, string) (bool, error) {
	return false, nil
}
func (*rawRenewalCoordinator) OpenCircuit(context.Context, string, time.Duration) error {
	return nil
}
func (*rawRenewalCoordinator) CloseCircuit(context.Context, string) error { return nil }

func (c *rawRenewalCoordinator) Renewals() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.renewals
}

func (c *rawRenewalCoordinator) SetFail() {
	c.mu.Lock()
	c.fail = true
	c.mu.Unlock()
}

func TestRawRequestLockRenewsWhileHeld(t *testing.T) {
	coordinator := &rawRenewalCoordinator{}
	cache := NewDefaultCache(objectstore.NewMemoryStore(), nil).WithCoordinator(coordinator)
	cache.lockRenewEvery = 5 * time.Millisecond

	_, release, err := cache.AcquireRequestLock(context.Background(), "large-object")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(18 * time.Millisecond)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if coordinator.Renewals() < 2 {
		t.Fatalf("request lock renewals=%d, want at least 2", coordinator.Renewals())
	}
}

func TestRawRequestLockCancelsWorkWhenRenewalFails(t *testing.T) {
	coordinator := &rawRenewalCoordinator{}
	coordinator.SetFail()
	cache := NewDefaultCache(objectstore.NewMemoryStore(), nil).WithCoordinator(coordinator).WithLockTiming(30*time.Millisecond, 5*time.Millisecond)

	workCtx, release, err := cache.AcquireRequestLock(context.Background(), "large-object")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-workCtx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("request work context was not cancelled after renewal failure")
	}
	if err := release(); err == nil || !strings.Contains(err.Error(), "renewal failed") {
		t.Fatalf("release error=%v, want renewal failure", err)
	}
}

type rawNoObjectGetStore struct {
	*objectstore.MemoryStore
	objectGets int
}

func (s *rawNoObjectGetStore) Get(ctx context.Context, key string) ([]byte, error) {
	if len(key) >= len("raw/objects/") && key[:len("raw/objects/")] == "raw/objects/" {
		s.objectGets++
		return nil, objectstore.ErrNotFound
	}
	return s.MemoryStore.Get(ctx, key)
}

func TestRawCacheLoadDoesNotMaterializeObjectBytes(t *testing.T) {
	store := &rawNoObjectGetStore{MemoryStore: objectstore.NewMemoryStore()}
	cache := NewDefaultCache(store, nil)
	key := cache.Key("downloads", "large.iso", "proxy", "https://proxy.example")
	if err := cache.Store(context.Background(), key, CachedContent{Body: []byte("artifact"), Repository: "downloads"}); err != nil {
		t.Fatal(err)
	}
	content, err := cache.Load(context.Background(), key)
	if err != nil {
		t.Fatalf("streaming cache load=%v", err)
	}
	if store.objectGets != 0 {
		t.Fatalf("cache load materialized object %d times", store.objectGets)
	}
	reader, size, err := content.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(reader, body); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if string(body) != "artifact" {
		t.Fatalf("streamed body=%q", body)
	}
}
