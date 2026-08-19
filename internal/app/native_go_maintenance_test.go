package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type failOnceGoDeleteStore struct {
	*MemoryOCIObjectStore
	fail bool
}

func (s *failOnceGoDeleteStore) Delete(ctx context.Context, key string) error {
	if s.fail {
		return errors.New("object store unavailable")
	}
	return s.MemoryOCIObjectStore.Delete(ctx, key)
}

func TestNativeGoMaintenanceRetriesDurablePublicationCleanup(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-reclaim", Name: "go-reclaim", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects := &failOnceGoDeleteStore{MemoryOCIObjectStore: NewMemoryOCIObjectStore(), fail: true}
	key := "native/go/sha256/" + strings.Repeat("a", 64)
	if err = objects.Put(ctx, key, []byte("orphan")); err != nil {
		t.Fatal(err)
	}
	job, err := enqueueGoPublicationReclaim(ctx, store, repo.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	metrics := &Metrics{}
	maintenance := NativeGoMaintenance{Store: store, Objects: objects, Metrics: metrics}
	if err = maintenance.RunReclaimJobs(ctx, 10); err == nil {
		t.Fatal("first reclaim must fail")
	}
	current, err := store.GetLifecycleJob(ctx, repo.ID, job.ID)
	if err != nil || current.State != repository.LifecycleJobRetrying {
		t.Fatalf("retrying job=%#v err=%v", current, err)
	}
	if _, err = store.RunLifecycleJobNow(ctx, repo.ID, job.ID); err != nil {
		t.Fatal(err)
	}
	objects.fail = false
	if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, key); err == nil {
		t.Fatal("orphan Go publication object remains")
	}
	current, err = store.GetLifecycleJob(ctx, repo.ID, job.ID)
	if err != nil || current.State != repository.LifecycleJobCompleted {
		t.Fatalf("completed job=%#v err=%v", current, err)
	}
	if metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationGo][backgroundOperationStarted].Load() != 2 ||
		metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationGo][backgroundOperationFailed].Load() != 1 ||
		metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationGo][backgroundOperationCompleted].Load() != 1 ||
		metrics.backgroundInFlight[backgroundOperationLifecycle][backgroundOperationGo].Load() != 0 {
		t.Fatal("Go lifecycle metrics were not recorded")
	}
}

func TestNativeGoMaintenanceKeepsReferencedPublicationObject(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-referenced", Name: "go-referenced", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	key, digest := "native/go/sha256/"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("b", 64)
	asset := repository.GoModuleAsset{
		RepositoryID: repo.ID, Module: "example.com/team/referenced", Version: "v1.0.0", Kind: "zip",
		ObjectKey: key, Digest: digest, Size: 7,
	}
	if _, err = store.PutGoModuleVersion(ctx, repository.GoModuleVersion{
		RepositoryID: repo.ID, Module: asset.Module, Version: asset.Version, PublishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CacheGoModuleAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	if err = objects.Put(ctx, key, []byte("visible")); err != nil {
		t.Fatal(err)
	}
	job, err := enqueueGoPublicationReclaim(ctx, store, repo.ID, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = (NativeGoMaintenance{Store: store, Objects: objects}).RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, key); err != nil {
		t.Fatalf("referenced object was deleted: %v", err)
	}
	current, err := store.GetLifecycleJob(ctx, repo.ID, job.ID)
	if err != nil || current.State != repository.LifecycleJobCompleted {
		t.Fatalf("completed referenced-object job=%#v err=%v", current, err)
	}
}
