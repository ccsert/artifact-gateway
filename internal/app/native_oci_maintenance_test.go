package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativeOCICollectorRetainsExpiredUploadTrace(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	now := time.Now().UTC()
	upload := repository.OCIUpload{ID: "expired-upload", RepositoryID: "repo", Name: "image", ObjectKey: "native/oci/uploads/expired-upload", State: "open", ExpiresAt: now.Add(-time.Minute)}
	if _, err := store.CreateOCIUpload(context.Background(), upload); err != nil {
		t.Fatal(err)
	}
	if err := objects.PutReader(context.Background(), upload.ObjectKey, bytes.NewReader([]byte("partial")), 7); err != nil {
		t.Fatal(err)
	}
	partKey := upload.ObjectKey + ".parts/00000000000000000007"
	if err := objects.PutReader(context.Background(), partKey, bytes.NewReader([]byte("chunk")), 5); err != nil {
		t.Fatal(err)
	}
	maintenance := NativeOCIMaintenance{Store: store, Objects: objects, Now: func() time.Time { return now }}
	if err := maintenance.Schedule(context.Background()); err != nil {
		t.Fatal(err)
	}
	if body, err := objects.Get(context.Background(), upload.ObjectKey); err != nil || string(body) != "partial" {
		t.Fatalf("scheduler changed partial object body=%q err=%v", body, err)
	}
	remaining, err := store.ListUncollectedOCIUploads(context.Background(), 10)
	if err != nil || len(remaining) != 1 {
		t.Fatalf("scheduled uncollected=%#v err=%v", remaining, err)
	}
	if err := maintenance.RunReclaimJobs(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Get(context.Background(), upload.ObjectKey); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("partial object err=%v", err)
	}
	if _, err := objects.Get(context.Background(), partKey); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("partial chunk err=%v", err)
	}
	remaining, err = store.ListUncollectedOCIUploads(context.Background(), 10)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("uncollected=%#v err=%v", remaining, err)
	}
	state, err := store.GetOCIUpload(context.Background(), upload.ID)
	if err != nil || state.State != "expired" || state.CollectedAt.IsZero() {
		t.Fatalf("state=%#v err=%v", state, err)
	}
}

func TestNativeOCICollectorCollectsUnclaimedObjectIntent(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	intent := repository.OCIObjectIntent{RepositoryID: "repo", ObjectKey: "native/oci/manifests/orphan", Digest: "sha256:" + strings.Repeat("a", 64), Size: 6}
	if err := store.StageOCIObjectIntent(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(context.Background(), intent.ObjectKey, []byte("orphan")); err != nil {
		t.Fatal(err)
	}
	metrics := &Metrics{}
	maintenance := NativeOCIMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }, Metrics: metrics}
	if err := maintenance.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Get(context.Background(), intent.ObjectKey); !errors.Is(err, errOCICacheMiss) {
		t.Fatalf("orphan object err=%v", err)
	}
	remaining, err := store.ListUnclaimedOCIObjectIntents(context.Background(), time.Now().UTC().Add(48*time.Hour), 10)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("unclaimed=%#v err=%v", remaining, err)
	}
	if metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationOCI][backgroundOperationStarted].Load() != 1 ||
		metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationOCI][backgroundOperationCompleted].Load() != 1 ||
		metrics.backgroundInFlight[backgroundOperationLifecycle][backgroundOperationOCI].Load() != 0 {
		t.Fatalf("OCI lifecycle metrics were not recorded")
	}
}

func TestNativeOCICollectorRechecksIntentAfterAcquiringObjectLock(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	intent := repository.OCIObjectIntent{RepositoryID: "repo", ObjectKey: "native/oci/manifests/published", Digest: "sha256:" + strings.Repeat("b", 64), Size: 9}
	if err := store.StageOCIObjectIntent(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(context.Background(), intent.ObjectKey, []byte("published")); err != nil {
		t.Fatal(err)
	}
	release, err := store.LockOCIObject(context.Background(), intent.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		finished <- (NativeOCIMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }}).Collect(context.Background())
	}()
	// The collector has selected the old intent but cannot evaluate it until
	// publication releases the same object lock.
	time.Sleep(10 * time.Millisecond)
	if _, err := store.PutOCIManifest(context.Background(), repository.OCIManifest{RepositoryID: "repo", Name: "image", Digest: intent.Digest, ObjectKey: intent.ObjectKey, MediaType: "application/json", Size: intent.Size}, intent.Digest); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if body, err := objects.Get(context.Background(), intent.ObjectKey); err != nil || string(body) != "published" {
		t.Fatalf("published object body=%q err=%v", body, err)
	}
}
