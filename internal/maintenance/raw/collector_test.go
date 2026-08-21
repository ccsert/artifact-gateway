package raw

import (
	"context"
	"sort"
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

func (s *testObjectStore) List(_ context.Context, prefix string) ([]string, error) {
	keys := make([]string, 0)
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
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

func TestCollectorCancelsExpiredRawUploadAndDeletesAllChunks(t *testing.T) {
	now := time.Now().UTC()
	store := repository.NewMemoryStore()
	upload := repository.RawUpload{
		ID: "expired-upload", RepositoryID: "repo", Path: "release.iso",
		ObjectKey: "native/raw/uploads/expired-upload", State: "open", ExpiresAt: now.Add(-time.Minute),
	}
	if _, err := store.CreateRawUpload(context.Background(), upload); err != nil {
		t.Fatal(err)
	}
	objects := &testObjectStore{objects: map[string]bool{
		upload.ObjectKey: true,
		upload.ObjectKey + ".parts/00000000000000000000": true,
		upload.ObjectKey + ".parts/00000000000000000005": true,
	}}

	if err := (Collector{Store: store, Objects: objects, Now: func() time.Time { return now }}).Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(objects.objects) != 0 {
		t.Fatalf("expired Raw upload objects remain: %v", objects.objects)
	}
	got, err := store.GetRawUpload(context.Background(), upload.ID)
	if err != nil || got.State != "cancelled" {
		t.Fatalf("expired Raw upload state=%q err=%v", got.State, err)
	}
}
