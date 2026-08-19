package app

import (
	"context"
	"encoding/json"
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

type failOnceGoCollectedStore struct {
	*repository.MemoryStore
	fail bool
}

func (s *failOnceGoCollectedStore) MarkGoModuleObjectCollected(ctx context.Context, objectKey string) error {
	if s.fail {
		s.fail = false
		return errors.New("database unavailable after object delete")
	}
	return s.MemoryStore.MarkGoModuleObjectCollected(ctx, objectKey)
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

func TestNativeGoMaintenanceReclaimsTombstonedVersionAfterRecoveryWindow(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "go-retention-reclaim", Name: "go-retention-reclaim", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	modulePath, version := "example.com/team/reclaim", "v1.0.0"
	now := time.Now().UTC()
	assets := []repository.GoModuleAsset{
		{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "info", Digest: "sha256:" + strings.Repeat("1", 64), ObjectKey: "native/go/reclaim/info", Size: 1},
		{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "mod", Digest: "sha256:" + strings.Repeat("2", 64), ObjectKey: "native/go/reclaim/mod", Size: 2},
		{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "zip", Digest: "sha256:" + strings.Repeat("3", 64), ObjectKey: "native/go/reclaim/zip", Size: 3},
	}
	if _, _, err = store.PublishGoModule(ctx, repository.GoModulePublication{
		Version: repository.GoModuleVersion{RepositoryID: repo.ID, Module: modulePath, Version: version, PublishedAt: now},
		Assets:  assets,
	}); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	for _, asset := range assets {
		if err = objects.Put(ctx, asset.ObjectKey, []byte(asset.Kind)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, version); err != nil {
		t.Fatal(err)
	}

	maintenance := NativeGoMaintenance{Store: store, Objects: objects, RecoveryWindow: 24 * time.Hour, Now: func() time.Time { return now.Add(23 * time.Hour) }}
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if _, err = objects.Get(ctx, asset.ObjectKey); err != nil {
			t.Fatalf("object %s was reclaimed inside recovery window: %v", asset.Kind, err)
		}
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 6 || capacity.ObjectCount != 3 {
		t.Fatalf("capacity before reclaim=%#v err=%v", capacity, err)
	}

	if err = maintenance.EnqueueReclaimJobs(ctx, now.Add(25*time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RestoreGoModuleVersion(ctx, repo.ID, modulePath, version); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, err = store.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, version); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if _, err = objects.Get(ctx, asset.ObjectKey); err != nil {
			t.Fatalf("stale reclaim intent bypassed the new recovery window for %s: %v", asset.Kind, err)
		}
	}

	maintenance.Now = func() time.Time { return now.Add(25 * time.Hour) }
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if _, err = objects.Get(ctx, asset.ObjectKey); err == nil {
			t.Fatalf("object %s remains after delayed reclaim", asset.Kind)
		}
	}
	capacity, err = store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 0 || capacity.ObjectCount != 0 {
		t.Fatalf("capacity after reclaim=%#v err=%v", capacity, err)
	}
	if _, err = store.RestoreGoModuleVersion(ctx, repo.ID, modulePath, version); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("collected Go version restored: %v", err)
	}
}

func TestNativeGoMaintenanceCollectsTombstonedReferenceButKeepsSharedObject(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repoA, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "go-shared-a", Name: "go-shared-a", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	repoB, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "go-shared-b", Name: "go-shared-b", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	modulePath, version := "example.com/team/shared-maintenance", "v1.0.0"
	assetsFor := func(repositoryID string) []repository.GoModuleAsset {
		return []repository.GoModuleAsset{
			{RepositoryID: repositoryID, Module: modulePath, Version: version, Kind: "info", Digest: "sha256:" + strings.Repeat("4", 64), ObjectKey: "native/go/shared/info", Size: 1},
			{RepositoryID: repositoryID, Module: modulePath, Version: version, Kind: "mod", Digest: "sha256:" + strings.Repeat("5", 64), ObjectKey: "native/go/shared/mod", Size: 2},
			{RepositoryID: repositoryID, Module: modulePath, Version: version, Kind: "zip", Digest: "sha256:" + strings.Repeat("6", 64), ObjectKey: "native/go/shared/zip", Size: 3},
		}
	}
	for _, repo := range []repository.HostedRepository{repoA, repoB} {
		if _, _, err = store.PublishGoModule(ctx, repository.GoModulePublication{
			Version: repository.GoModuleVersion{RepositoryID: repo.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC()},
			Assets:  assetsFor(repo.ID),
		}); err != nil {
			t.Fatal(err)
		}
	}
	objects := NewMemoryOCIObjectStore()
	for _, asset := range assetsFor(repoA.ID) {
		if err = objects.Put(ctx, asset.ObjectKey, []byte(asset.Kind)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.TombstoneGoModuleVersion(ctx, repoA.ID, modulePath, version); err != nil {
		t.Fatal(err)
	}
	maintenance := NativeGoMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }}
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	for _, asset := range assetsFor(repoA.ID) {
		if _, err = objects.Get(ctx, asset.ObjectKey); err != nil {
			t.Fatalf("shared object %s was physically deleted: %v", asset.Kind, err)
		}
	}
	capacityA, err := store.GetRepositoryCapacity(ctx, repoA.ID)
	if err != nil || capacityA.UsedBytes != 0 || capacityA.ObjectCount != 0 {
		t.Fatalf("tombstoned shared-reference capacity=%#v err=%v", capacityA, err)
	}
	capacityB, err := store.GetRepositoryCapacity(ctx, repoB.ID)
	if err != nil || capacityB.UsedBytes != 6 || capacityB.ObjectCount != 3 {
		t.Fatalf("visible shared-reference capacity=%#v err=%v", capacityB, err)
	}
	if _, err = store.RestoreGoModuleVersion(ctx, repoA.ID, modulePath, version); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("collected shared reference restored: %v", err)
	}
}

func TestNativeGoMaintenanceFailsRestoreClosedAfterDeleteBeforeCollectedMark(t *testing.T) {
	ctx := context.Background()
	store := &failOnceGoCollectedStore{MemoryStore: repository.NewMemoryStore(), fail: true}
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "go-collecting-fence", Name: "go-collecting-fence", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	modulePath, version := "example.com/team/collecting-fence", "v1.0.0"
	assets := []repository.GoModuleAsset{
		{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "info", Digest: "sha256:" + strings.Repeat("7", 64), ObjectKey: "native/go/fence/info", Size: 1},
		{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "mod", Digest: "sha256:" + strings.Repeat("8", 64), ObjectKey: "native/go/fence/mod", Size: 2},
		{RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: "zip", Digest: "sha256:" + strings.Repeat("9", 64), ObjectKey: "native/go/fence/zip", Size: 3},
	}
	if _, _, err = store.PublishGoModule(ctx, repository.GoModulePublication{
		Version: repository.GoModuleVersion{RepositoryID: repo.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC()}, Assets: assets,
	}); err != nil {
		t.Fatal(err)
	}
	objects := NewMemoryOCIObjectStore()
	for _, asset := range assets {
		if err = objects.Put(ctx, asset.ObjectKey, []byte(asset.Kind)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, version); err != nil {
		t.Fatal(err)
	}
	maintenance := NativeGoMaintenance{Store: store, Objects: objects}
	if err = maintenance.EnqueueReclaimJobs(ctx, time.Now().UTC().Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	if err = maintenance.RunReclaimJobs(ctx, 10); err == nil {
		t.Fatal("collected metadata failure was not surfaced")
	}
	if _, err = store.RestoreGoModuleVersion(ctx, repo.ID, modulePath, version); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("collecting version restored after bytes were deleted: %v", err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.State == repository.LifecycleJobRetrying {
			if _, err = store.RunLifecycleJobNow(ctx, repo.ID, job.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil || capacity.UsedBytes != 0 || capacity.ObjectCount != 0 {
		t.Fatalf("capacity after collecting retry=%#v err=%v", capacity, err)
	}
}

func TestNativeGoMaintenanceEnqueuesBoundedPagePastFailedReclaimHead(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "go-reclaim-pages", Name: "go-reclaim-pages", Format: repository.FormatGo, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	modulePath := "example.com/team/reclaim-pages"
	for versionIndex, version := range []string{"v1.0.0", "v1.1.0"} {
		assets := make([]repository.GoModuleAsset, 0, 3)
		for kindIndex, kind := range []string{"info", "mod", "zip"} {
			digitByte := string(rune('a' + versionIndex*3 + kindIndex))
			assets = append(assets, repository.GoModuleAsset{
				RepositoryID: repo.ID, Module: modulePath, Version: version, Kind: kind,
				Digest: "sha256:" + strings.Repeat(digitByte, 64), ObjectKey: "native/go/reclaim-pages/" + version + "/" + kind, Size: 1,
			})
		}
		if _, _, err = store.PublishGoModule(ctx, repository.GoModulePublication{
			Version: repository.GoModuleVersion{RepositoryID: repo.ID, Module: modulePath, Version: version, PublishedAt: time.Now().UTC()}, Assets: assets,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err = store.TombstoneGoModuleVersion(ctx, repo.ID, modulePath, version); err != nil {
			t.Fatal(err)
		}
	}
	before := time.Now().UTC().Add(time.Hour)
	head, err := store.ListReclaimableGoModuleObjects(ctx, before, 1, "")
	if err != nil || len(head) != 1 {
		t.Fatalf("reclaim head=%#v err=%v", head, err)
	}
	payload, err := json.Marshal(goReclaimPayload{Format: repository.FormatGo, ObjectKey: head[0].ObjectKey, Tombstone: true, TombstonedAt: head[0].TombstonedAt})
	if err != nil {
		t.Fatal(err)
	}
	idempotencyKey := "go-tombstone-object:" + head[0].ObjectKey + ":" + head[0].TombstonedAt.UTC().Format(time.RFC3339Nano)
	failed, _, err := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{
		ID: "failed-reclaim-head", RepositoryID: repo.ID, Kind: repository.LifecycleJobReclaim,
		IdempotencyKey: idempotencyKey, Payload: payload, MaxAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimLifecycleJobsByKindAndFormat(ctx, repository.LifecycleJobReclaim, repository.FormatGo, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != failed.ID {
		t.Fatalf("claimed failed head=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, failed.ID, claimed[0].LeaseToken, "terminal object-store failure"); err != nil {
		t.Fatal(err)
	}

	if err = (NativeGoMaintenance{Store: store}).EnqueueReclaimJobs(ctx, before, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, repo.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("bounded enqueue behind failed head: jobs=%d want=2", len(jobs))
	}
}
