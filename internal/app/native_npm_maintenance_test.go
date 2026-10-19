package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
)

func TestNativeNPMMaintenanceReclaimsUnreferencedTombstone(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "npm-reclaim", Name: "npm-reclaim", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("npm artifact")
	version := repository.NPMVersion{
		RepositoryID: repo.ID, PackageName: "widget", Version: "1.0.0",
		Digest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Integrity: "sha512-YQ==", Shasum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TarballName: "widget-1.0.0.tgz", ObjectKey: "native/npm/sha256/reclaim", Size: int64(len(body)),
		Manifest: json.RawMessage(`{"name":"widget","version":"1.0.0"}`),
	}
	if err = objects.Put(ctx, version.ObjectKey, body); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishNPMVersion(ctx, version, map[string]string{"latest": version.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneNPMVersion(ctx, repo.ID, version.PackageName, version.Version); err != nil {
		t.Fatal(err)
	}
	maintenance := NativeNPMMaintenance{Store: store, Objects: objects, Now: func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }}
	if err = maintenance.Collect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = objects.Get(ctx, version.ObjectKey); err == nil {
		t.Fatal("reclaimed npm object remained in object storage")
	}
	if _, err = store.RestoreNPMVersion(ctx, repository.NPMRestoreRequest{RepositoryID: repo.ID, PackageName: version.PackageName, Version: version.Version}); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("collected npm version restored: %v", err)
	}
}

type npmCollectionMarkFailure struct{ *repository.MemoryStore }

func (s npmCollectionMarkFailure) MarkNPMObjectCollected(context.Context, string) error {
	return errors.New("fixture metadata commit unavailable after S3 delete")
}

func TestNativeNPMRestoreCannotResurrectAfterReclaimPartialFailure(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	version := repository.NPMVersion{RepositoryID: "npm-partial-reclaim", PackageName: "widget", Version: "1.0.0", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "native/npm/sha256/partial-reclaim", Size: 3, Manifest: []byte(`{"name":"widget","version":"1.0.0"}`)}
	if err := objects.Put(ctx, version.ObjectKey, []byte("npm")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishNPMVersion(ctx, version, map[string]string{"latest": version.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.TombstoneNPMVersion(ctx, version.RepositoryID, version.PackageName, version.Version); err != nil {
		t.Fatal(err)
	}
	maintenance := NativeNPMMaintenance{Store: npmCollectionMarkFailure{store}, Objects: objects, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }}
	if err := maintenance.Collect(ctx); err == nil {
		t.Fatal("fixture failed to inject the metadata failure")
	}
	if _, err := objects.Get(ctx, version.ObjectKey); err == nil {
		t.Fatal("fixture did not delete S3 bytes")
	}
	if _, err := store.RestoreNPMVersion(ctx, repository.NPMRestoreRequest{RepositoryID: version.RepositoryID, PackageName: version.PackageName, Version: version.Version}); !errors.Is(err, repository.ErrDisabled) {
		t.Fatalf("restore after lost collection commit must fail closed, got %v", err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, version.RepositoryID, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("retry inventory=%#v %v", jobs, err)
	}
	// A failed attempt is queued with backoff. Operator cancellation/retry
	// makes it immediately runnable through the real control contract.
	if _, err = store.CancelLifecycleJob(ctx, version.RepositoryID, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RetryLifecycleJob(ctx, version.RepositoryID, jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	maintenance.Store = store
	if err = maintenance.RunReclaimJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	pkg, err := store.GetNPMPackage(ctx, version.RepositoryID, version.PackageName)
	if err != nil || len(pkg.TombstonedVersions) != 1 || pkg.TombstonedVersions[0].CollectedAt.IsZero() || !pkg.TombstonedVersions[0].CollectingAt.IsZero() {
		t.Fatalf("retry did not finish collection: %#v %v", pkg, err)
	}
}

func TestNativeNPMMaintenanceSerializesReclaimAndRestore(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := objectstore.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "npm-reclaim-race", Name: "npm-reclaim-race", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	version := repository.NPMVersion{
		RepositoryID: repo.ID, PackageName: "widget", Version: "1.0.0",
		Digest:      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TarballName: "widget-1.0.0.tgz", ObjectKey: "native/npm/sha256/reclaim-race", Size: 3,
		Manifest: json.RawMessage(`{"name":"widget","version":"1.0.0"}`),
	}
	if err = objects.Put(ctx, version.ObjectKey, []byte("npm")); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishNPMVersion(ctx, version, map[string]string{"latest": version.Version}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.TombstoneNPMVersion(ctx, repo.ID, version.PackageName, version.Version); err != nil {
		t.Fatal(err)
	}

	blocking := blockingLifecycleDeleteStore{ObjectStore: objects, entered: make(chan struct{}, 1), release: make(chan struct{})}
	maintenance := NativeNPMMaintenance{Store: store, Objects: blocking, Now: func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }}
	if err = maintenance.EnqueueReclaimJobs(ctx, time.Now().UTC().Add(time.Hour), 10); err != nil {
		t.Fatal(err)
	}
	reclaimResult := make(chan error, 1)
	go func() { reclaimResult <- maintenance.RunReclaimJobs(ctx, 10) }()
	select {
	case <-blocking.entered:
	case <-time.After(time.Second):
		t.Fatal("npm reclaim did not reach object deletion")
	}

	restoreStarted := make(chan struct{})
	restoreResult := make(chan error, 1)
	go func() {
		close(restoreStarted)
		_, restoreErr := store.RestoreNPMVersion(ctx, repository.NPMRestoreRequest{RepositoryID: repo.ID, PackageName: version.PackageName, Version: version.Version})
		restoreResult <- restoreErr
	}()
	<-restoreStarted
	select {
	case restoreErr := <-restoreResult:
		t.Fatalf("npm restore bypassed object lock: %v", restoreErr)
	case <-time.After(100 * time.Millisecond):
	}
	close(blocking.release)
	if err = <-reclaimResult; err != nil {
		t.Fatal(err)
	}
	if restoreErr := <-restoreResult; !errors.Is(restoreErr, repository.ErrDisabled) {
		t.Fatalf("npm restore after reclaim error=%v", restoreErr)
	}
}
