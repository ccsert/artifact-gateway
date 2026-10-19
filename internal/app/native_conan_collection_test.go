package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type conanCollectionWorkerStore interface {
	repository.HostedRepositoryStore
	repository.NativeConanStore
	repository.LifecycleJobStore
	repository.ConanObjectCollectionStore
}

type conanCollectionDeleteBarrier struct {
	ObjectStore
	target      string
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
	calls       atomic.Int64
	failureMode string
}

func (b *conanCollectionDeleteBarrier) Delete(ctx context.Context, key string) error {
	if key != b.target {
		return b.ObjectStore.Delete(ctx, key)
	}
	b.calls.Add(1)
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	if b.failureMode == "delete_before" {
		return errors.New("fixture deletion failed before byte deletion")
	}
	if err := b.ObjectStore.Delete(ctx, key); err != nil {
		return err
	}
	if b.failureMode == "delete_after" {
		return errors.New("fixture deletion response lost after byte deletion")
	}
	return nil
}

type conanCollectionFinalizeFailure struct {
	repository.ConanObjectCollectionStore
}

func (s conanCollectionFinalizeFailure) CompleteConanObjectCollection(context.Context, string) error {
	return errors.New("fixture collection finalization unavailable")
}

func TestNativeConanCollectionFencesSharedRestoreAndRetries(t *testing.T) {
	exerciseConanCollectionWorker(t, func(t *testing.T) (conanCollectionWorkerStore, ObjectStore, func(*testing.T, conanCollectionWorkerStore, string) conanCollectionWorkerStore) {
		return repository.NewMemoryStore(), objectstore.NewMemoryStore(), func(_ *testing.T, store conanCollectionWorkerStore, _ string) conanCollectionWorkerStore {
			return store
		}
	})
}

type conanCollectionWorkerFactory func(*testing.T) (conanCollectionWorkerStore, ObjectStore, func(*testing.T, conanCollectionWorkerStore, string) conanCollectionWorkerStore)

func exerciseConanCollectionWorker(t *testing.T, factory conanCollectionWorkerFactory) {
	t.Helper()
	for _, kind := range []string{"recipe", "package"} {
		for _, mode := range []string{"collector_wins", "reference_wins", "delete_before", "delete_after", "finalize_failure"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				store, objects, reopen := factory(t)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				f := newConanCollectionWorkerFixture(t, store, objects, kind)
				f.tombstone(t, store, f.source.ID)
				f.tombstone(t, store, f.target.ID)
				if mode == "reference_wins" {
					if err := f.restore(ctx, store, f.target.ID); err != nil {
						t.Fatal(err)
					}
				}
				payload, err := json.Marshal(conanReclaimPayload{Format: repository.FormatConan, ObjectKey: f.asset.ObjectKey})
				if err != nil {
					t.Fatal(err)
				}
				job, _, err := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: f.source.ID, Kind: repository.LifecycleJobReclaim, IdempotencyKey: "conan-object:" + f.asset.ObjectKey, Payload: payload})
				if err != nil {
					t.Fatal(err)
				}
				gate := &conanCollectionDeleteBarrier{ObjectStore: objects, target: f.asset.ObjectKey, entered: make(chan struct{}), release: make(chan struct{}), failureMode: mode}
				var released sync.Once
				release := func() { released.Do(func() { close(gate.release) }) }
				defer release()
				var collections repository.ConanObjectCollectionStore = store
				if mode == "finalize_failure" {
					collections = conanCollectionFinalizeFailure{store}
				}
				done := make(chan error, 1)
				go func() {
					done <- (NativeConanMaintenance{Store: store, Collections: collections, Objects: gate}).RunReclaimJobs(ctx, 1)
				}()
				finished := false
				defer func() {
					release()
					cancel()
					if !finished {
						select {
						case <-done:
						case <-time.After(3 * time.Second):
							t.Error("owned reclaim worker did not stop")
						}
					}
				}()
				if mode == "reference_wins" {
					select {
					case err = <-done:
						finished = true
					case <-gate.entered:
						t.Fatal("collector reached physical delete despite a shared target reference")
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					if err != nil || gate.calls.Load() != 0 {
						t.Fatalf("reference winner worker: deletes=%d err=%v", gate.calls.Load(), err)
					}
					if body, err := objects.Get(ctx, f.asset.ObjectKey); err != nil || string(body) != string(f.body) {
						t.Fatalf("reference winner lost bytes: %q err=%v", body, err)
					}
					// A completed skip is history, not a permanent veto on later
					// reclamation after this reference is removed again.
					for round := 0; round < 2; round++ {
						f.tombstone(t, store, f.target.ID)
						maintenance := NativeConanMaintenance{Store: store, Collections: store, Objects: objects}
						var wg sync.WaitGroup
						enqueueErrors := make(chan error, 8)
						for i := 0; i < 8; i++ {
							wg.Add(1)
							go func() {
								defer wg.Done()
								enqueueErrors <- maintenance.EnqueueReclaimJobs(ctx, time.Now().Add(time.Hour), 1000)
							}()
						}
						wg.Wait()
						close(enqueueErrors)
						for err := range enqueueErrors {
							if err != nil {
								t.Fatal(err)
							}
						}
						jobs, err := store.ListLifecycleJobs(ctx, f.source.ID, 100)
						if err != nil {
							t.Fatal(err)
						}
						active, predecessors := 0, 0
						for _, candidate := range jobs {
							var p conanReclaimPayload
							if json.Unmarshal(candidate.Payload, &p) != nil || p.ObjectKey != f.asset.ObjectKey {
								continue
							}
							if candidate.State == repository.LifecycleJobCompleted {
								predecessors++
							}
							if candidate.State == repository.LifecycleJobPending || candidate.State == repository.LifecycleJobRunning || candidate.State == repository.LifecycleJobRetrying {
								active++
							}
						}
						if active != 1 || predecessors != round+1 {
							t.Fatalf("concurrent schedulers did not converge: active=%d preserved=%d jobs=%+v", active, predecessors, jobs)
						}
						if round == 0 {
							if err := f.restore(ctx, store, f.target.ID); err != nil {
								t.Fatal(err)
							}
						}
						if err := maintenance.RunReclaimJobs(ctx, 1000); err != nil {
							t.Fatal(err)
						}
						_, readErr := objects.Get(ctx, f.asset.ObjectKey)
						if (round == 0 && readErr != nil) || (round == 1 && readErr == nil) {
							t.Fatalf("repeat reference winner/collector winner bytes: round=%d err=%v", round, readErr)
						}
					}
					return
				}
				select {
				case <-gate.entered:
				case err = <-done:
					finished = true
					t.Fatalf("worker did not reach physical delete: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if body, err := objects.Get(ctx, f.asset.ObjectKey); err != nil || string(body) != string(f.body) {
					t.Fatalf("barrier did not precede physical deletion: %q err=%v", body, err)
				}
				if err := f.restore(ctx, store, f.target.ID); !errors.Is(err, repository.ErrNotFound) {
					t.Fatalf("shared restore crossed durable delete fence before byte deletion: %v", err)
				}
				release()
				select {
				case err = <-done:
					finished = true
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if mode == "collector_wins" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					if err == nil {
						t.Fatal("injected uncertain deletion/finalization was reported as success")
					}
					jobState, err := store.GetLifecycleJob(ctx, f.source.ID, job.ID)
					if err != nil || jobState.State != repository.LifecycleJobRetrying {
						t.Fatalf("failed collection did not preserve retryable job: %+v err=%v", jobState, err)
					}
					// Reopen the durable store (and instantiate a fresh worker). The
					// target must remain hidden even when Delete may already have won.
					store = reopen(t, store, f.asset.ObjectKey)
					if err := f.restore(ctx, store, f.target.ID); !errors.Is(err, repository.ErrNotFound) {
						t.Fatalf("worker/store restart released collecting fence: %v", err)
					}
					candidates, err := store.ListReclaimableConanObjects(ctx, time.Now().Add(time.Hour), 1000)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, candidate := range candidates {
						if candidate.ObjectKey == f.asset.ObjectKey && !candidate.CollectingAt.IsZero() && candidate.CollectedAt.IsZero() {
							found = true
						}
					}
					if !found {
						t.Fatal("restart lost durable collecting marker or reclaim retry eligibility")
					}
					if _, err = store.RunLifecycleJobNow(ctx, f.source.ID, job.ID); err != nil {
						t.Fatal(err)
					}
					if err := (NativeConanMaintenance{Store: store, Collections: store, Objects: objects}).RunReclaimJobs(ctx, 1); err != nil {
						t.Fatalf("fresh worker could not retry uncertain deletion: %v", err)
					}
				}
				if _, err := objects.Get(ctx, f.asset.ObjectKey); err == nil {
					t.Fatal("successful collection left object bytes")
				}
				if err := f.restore(ctx, store, f.target.ID); !errors.Is(err, repository.ErrNotFound) {
					t.Fatalf("collected target restored after completion: %v", err)
				}
				completed, err := store.GetLifecycleJob(ctx, f.source.ID, job.ID)
				if err != nil || completed.State != repository.LifecycleJobCompleted {
					t.Fatalf("collection did not complete durable job: %+v err=%v", completed, err)
				}
			})
		}
	}
}

type conanCollectionWorkerFixture struct {
	source, target repository.HostedRepository
	recipe         repository.ConanRecipeRevision
	pkg            repository.ConanPackageRevision
	asset          repository.ConanAsset
	body           []byte
	kind           string
}

func newConanCollectionWorkerFixture(t *testing.T, store conanCollectionWorkerStore, objects ObjectStore, kind string) conanCollectionWorkerFixture {
	t.Helper()
	ctx := context.Background()
	f := conanCollectionWorkerFixture{kind: kind}
	for _, repo := range []*repository.HostedRepository{&f.source, &f.target} {
		created, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-collection-" + uuid.NewString(), Format: repository.FormatConan})
		if err != nil {
			t.Fatal(err)
		}
		*repo = created
	}
	stage := func(name string) (repository.ConanAsset, []byte) {
		body := []byte("owned Conan collection " + name + " " + uuid.NewString())
		digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
		asset := repository.ConanAsset{RepositoryID: f.source.ID, Reference: "pkg/1.0/_/_", RecipeRevision: "rrev", Path: name, ObjectKey: "native/conan/collection/" + uuid.NewString(), Digest: digest, Size: int64(len(body))}
		if err := objects.Put(ctx, asset.ObjectKey, body); err != nil {
			t.Fatal(err)
		}
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: f.source.ID, ObjectKey: asset.ObjectKey, Digest: digest, Size: asset.Size}); err != nil {
			t.Fatal(err)
		}
		return asset, body
	}
	recipeAsset, recipeBody := stage("conanfile.py")
	f.recipe = repository.ConanRecipeRevision{RepositoryID: f.source.ID, Reference: recipeAsset.Reference, Revision: recipeAsset.RecipeRevision, Digest: recipeAsset.Digest}
	if _, err := store.PutConanRecipeRevision(ctx, f.recipe, []repository.ConanAsset{recipeAsset}); err != nil {
		t.Fatal(err)
	}
	f.asset, f.body = recipeAsset, recipeBody
	if kind == "package" {
		asset, body := stage("conan_package.tgz")
		asset.PackageID, asset.PackageRevision = "package-id", "prev"
		f.pkg = repository.ConanPackageRevision{RepositoryID: f.source.ID, Reference: asset.Reference, RecipeRevision: asset.RecipeRevision, PackageID: asset.PackageID, Revision: asset.PackageRevision, Digest: asset.Digest}
		if _, err := store.PutConanPackageRevision(ctx, f.pkg, []repository.ConanAsset{asset}); err != nil {
			t.Fatal(err)
		}
		f.asset, f.body = asset, body
	}
	if _, err := store.PromoteConanRecipeRevision(ctx, repository.ConanPromotion{SourceRepositoryID: f.source.ID, TargetRepositoryID: f.target.ID, Reference: f.recipe.Reference, Revision: f.recipe.Revision, Digest: f.recipe.Digest}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f conanCollectionWorkerFixture) tombstone(t *testing.T, store repository.NativeConanStore, repo string) {
	t.Helper()
	var err error
	if f.kind == "recipe" {
		_, err = store.TombstoneConanRecipeRevision(context.Background(), repo, f.recipe.Reference, f.recipe.Revision)
	} else {
		_, err = store.TombstoneConanPackageRevision(context.Background(), repo, f.pkg.Reference, f.pkg.RecipeRevision, f.pkg.PackageID, f.pkg.Revision)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func (f conanCollectionWorkerFixture) restore(ctx context.Context, store repository.NativeConanStore, repo string) error {
	if f.kind == "recipe" {
		_, err := store.RestoreConanRecipeRevision(ctx, repo, f.recipe.Reference, f.recipe.Revision)
		return err
	}
	_, err := store.RestoreConanPackageRevision(ctx, repo, f.pkg.Reference, f.pkg.RecipeRevision, f.pkg.PackageID, f.pkg.Revision)
	return err
}

func TestNativeConanCollectionBlocksRestoreAndHTTPReplayBeforeDelete(t *testing.T) {
	f := newConanUploadFixture(t)
	f.publish(t, "build-agent")
	ctx := context.Background()
	asset, err := f.store.GetConanRecipeAsset(ctx, f.repo.ID, "pkg/1.0/_/_", f.revision, "conanfile.py")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.TombstoneConanRecipeRevision(ctx, f.repo.ID, asset.Reference, f.revision); err != nil {
		t.Fatal(err)
	}
	if collect, err := f.store.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || !collect {
		t.Fatalf("durable collector claim collect=%t err=%v", collect, err)
	}
	auditsBefore, err := f.store.ListAudits(ctx, repository.AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if body, err := f.objects.Get(ctx, asset.ObjectKey); err != nil || string(body) != "recipe" {
		t.Fatalf("fixture claim deleted bytes too early: %q err=%v", body, err)
	}
	if _, err := f.store.RestoreConanRecipeRevision(ctx, f.repo.ID, asset.Reference, f.revision); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("restore crossed collector claim: %v", err)
	}
	for _, name := range []string{"conanfile.py", "conanmanifest.txt"} {
		body := []byte("recipe")
		expected := http.StatusCreated
		if name == "conanmanifest.txt" {
			body = f.manifest
			expected = http.StatusConflict
		}
		// A new immutable upload key may be staged, but it cannot revive the
		// tombstoned identity or acknowledge a successful publication.
		if response := f.request(t, http.MethodPut, f.base+"/files/"+name, "build-agent", body); response.Code != expected || expected == http.StatusCreated && response.Header().Get("X-Artifact-Gateway-Publish-State") != "open" {
			t.Fatalf("HTTP replay crossed collector fence for %s: %d %s", name, response.Code, response.Body.String())
		}
		if response := f.request(t, http.MethodGet, f.base+"/files/"+name, "reader", nil); response.Code != http.StatusNotFound {
			t.Fatalf("fenced revision became visible before physical delete: %d %s", response.Code, response.Body.String())
		}
	}
	auditsAfter, err := f.store.ListAudits(ctx, repository.AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
	if err != nil || len(auditsAfter) != len(auditsBefore) {
		t.Fatalf("rejected publication wrote a success audit: before=%+v after=%+v err=%v", auditsBefore, auditsAfter, err)
	}
}
