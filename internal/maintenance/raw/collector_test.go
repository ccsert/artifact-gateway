package raw

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type testObjectStore struct{ objects map[string]bool }

func (s *testObjectStore) Delete(_ context.Context, key string) error {
	delete(s.objects, key)
	return nil
}

type testMetrics struct {
	started   uint64
	completed uint64
	inFlight  int64
}

func (m *testMetrics) RecordBackgroundOperation(kind string, format repository.Format, outcome string) {
	if kind != "lifecycle" || format != repository.FormatRaw {
		return
	}
	switch outcome {
	case "started":
		m.started++
	case "completed":
		m.completed++
	}
}

func (m *testMetrics) AddBackgroundOperationInFlight(kind string, format repository.Format, delta int64) {
	if kind == "lifecycle" && format == repository.FormatRaw {
		m.inFlight += delta
	}
}

func TestCollectorTracksAndCollectsUnreferencedObject(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := &testObjectStore{objects: map[string]bool{}}
	digest := "sha256:" + strings.Repeat("a", 64)
	key := "native/raw/sha256/" + strings.Repeat("a", 64)
	if err := store.StageRawObject(context.Background(), repository.RawObject{Digest: digest, ObjectKey: key, Size: 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutRawAsset(context.Background(), repository.RawAsset{RepositoryID: "repo", Path: "orphan.bin", Digest: digest, ObjectKey: key, Size: 6}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteRawAsset(context.Background(), "repo", "orphan.bin"); err != nil {
		t.Fatal(err)
	}
	objects.objects[key] = true
	metrics := &testMetrics{}
	if err := (Collector{Store: store, Objects: objects, Now: func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }, Metrics: metrics}).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if objects.objects[key] {
		t.Fatal("orphan object remains")
	}
	candidates, err := store.ListUnreferencedRawObjects(context.Background(), time.Now().UTC().Add(48*time.Hour), 10)
	if err != nil || len(candidates) != 0 {
		t.Fatalf("remaining raw collection candidates=%#v err=%v", candidates, err)
	}
	if metrics.started != 1 || metrics.completed != 1 || metrics.inFlight != 0 {
		t.Fatalf("lifecycle metrics started=%d completed=%d in_flight=%d", metrics.started, metrics.completed, metrics.inFlight)
	}
}
