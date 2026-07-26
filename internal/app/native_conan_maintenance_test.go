package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type failingConanDeleteStore struct {
	*MemoryOCIObjectStore
	fail bool
}

func (s *failingConanDeleteStore) Delete(ctx context.Context, key string) error {
	if s.fail {
		return errors.New("object store unavailable")
	}
	return s.MemoryOCIObjectStore.Delete(ctx, key)
}

func TestNativeConanMaintenanceRetriesFailedReclaimJob(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := &failingConanDeleteStore{MemoryOCIObjectStore: NewMemoryOCIObjectStore(), fail: true}
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "repo", Name: "conan", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	key, digest := "native/conan/reclaim", "sha256:"+strings.Repeat("a", 64)
	if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if err = objects.Put(ctx, key, []byte("old")); err != nil {
		t.Fatal(err)
	}
	revision := repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: "pkg/1/u/c", Revision: "rrev", Digest: digest}
	asset := repository.ConanAsset{RepositoryID: repo.ID, Reference: revision.Reference, RecipeRevision: revision.Revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 3}
	if _, err = store.PutConanRecipeRevision(ctx, revision, []repository.ConanAsset{asset}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneConanRecipeRevision(ctx, repo.ID, revision.Reference, revision.Revision); err != nil {
		t.Fatal(err)
	}
	metrics := &Metrics{}
	maintenance := NativeConanMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }, Metrics: metrics}
	if err = maintenance.Collect(ctx); err == nil {
		t.Fatal("first reclaim must fail")
	}
	objects.fail = false
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, key); err == nil {
		t.Fatal("reclaimed object remains")
	}
	if _, err = store.RestoreConanRecipeRevision(ctx, repo.ID, revision.Reference, revision.Revision); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("restore collected revision error=%v", err)
	}
	if metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationConan][backgroundOperationStarted].Load() != 2 ||
		metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationConan][backgroundOperationFailed].Load() != 1 ||
		metrics.backgroundOperations[backgroundOperationLifecycle][backgroundOperationConan][backgroundOperationCompleted].Load() != 1 ||
		metrics.backgroundInFlight[backgroundOperationLifecycle][backgroundOperationConan].Load() != 0 {
		t.Fatalf("Conan lifecycle metrics were not recorded")
	}
}
