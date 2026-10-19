package repository

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

type conanCollectionContractStore interface {
	conanManagedPublicationContractStore
	ConanObjectCollectionStore
}

func TestMemoryConanObjectCollectionFence(t *testing.T) {
	store := NewMemoryStore()
	exerciseConanObjectCollectionFence(t, store, func(t *testing.T, key string) ConanObjectIntent {
		t.Helper()
		store.mu.RLock()
		defer store.mu.RUnlock()
		return store.conanObjects[key]
	})
}

func exerciseConanObjectCollectionFence(t *testing.T, store conanCollectionContractStore, object func(*testing.T, string) ConanObjectIntent) {
	t.Helper()
	ctx := context.Background()
	for _, kind := range []string{"recipe", "package"} {
		t.Run(kind, func(t *testing.T) {
			t.Run("shared_target_reference_wins", func(t *testing.T) {
				f, target := newConanCollectionPromotion(t, store, kind)
				asset := f.assets(t)[0]
				if owner := object(t, asset.ObjectKey).RepositoryID; owner != f.repo.ID {
					t.Fatalf("promotion changed immutable object ownership: %s", owner)
				}
				conanCollectionTombstone(t, store, f.session, f.repo.ID)
				// A target reference protects a source-owned object after the source
				// revision has disappeared. A repository-only check is insufficient.
				if collect, err := store.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || collect {
					t.Fatalf("visible target lost its shared object: collect=%t err=%v", collect, err)
				}
				conanCollectionTombstone(t, store, f.session, target.ID)
				if err := conanCollectionRestore(ctx, store, f.session, target.ID); err != nil {
					t.Fatal(err)
				}
				if collect, err := store.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || collect {
					t.Fatalf("restore winner was not rechecked: collect=%t err=%v", collect, err)
				}
				if state := object(t, asset.ObjectKey); !state.CollectingAt.IsZero() || !state.CollectedAt.IsZero() {
					t.Fatalf("reference winner acquired a collection marker: %+v", state)
				}
			})
			t.Run("collector_wins_across_repositories", func(t *testing.T) {
				f, target := newConanCollectionPromotion(t, store, kind)
				assets := f.assets(t)
				asset := assets[0]
				conanCollectionTombstone(t, store, f.session, f.repo.ID)
				conanCollectionTombstone(t, store, f.session, target.ID)
				candidates, err := store.ListReclaimableConanObjects(ctx, time.Now().Add(time.Hour), 100000)
				if err != nil || !conanCollectionContains(candidates, asset.ObjectKey) {
					t.Fatalf("tombstoned %s object missing from candidates: err=%v", kind, err)
				}
				if kind == "package" {
					for _, repo := range []HostedRepository{f.repo, target} {
						recipe, err := store.GetConanRecipeRevision(ctx, repo.ID, f.session.Reference, f.session.RecipeRevision)
						if err != nil || recipe.State != "visible" {
							t.Fatalf("package candidate requires its parent to remain visible: %+v err=%v", recipe, err)
						}
					}
				}
				if err := store.CompleteConanObjectCollection(ctx, asset.ObjectKey); !errors.Is(err, ErrDisabled) {
					t.Fatalf("collection completed without durable claim: %v", err)
				}
				if collect, err := store.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || !collect {
					t.Fatalf("claim unreferenced object: collect=%t err=%v", collect, err)
				}
				claimed := object(t, asset.ObjectKey)
				if claimed.CollectingAt.IsZero() || !claimed.CollectedAt.IsZero() {
					t.Fatalf("claim was not persisted before physical delete: %+v", claimed)
				}
				// Reclaim retries retain the original irreversible boundary rather
				// than granting a fresh lease that can be released by a writer.
				if collect, err := store.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || !collect || !object(t, asset.ObjectKey).CollectingAt.Equal(claimed.CollectingAt) {
					t.Fatalf("claim retry changed the boundary: collect=%t err=%v", collect, err)
				}
				for _, completed := range []bool{false, true} {
					if completed {
						if err := store.CompleteConanObjectCollection(ctx, asset.ObjectKey); err != nil {
							t.Fatal(err)
						}
						if err := store.CompleteConanObjectCollection(ctx, asset.ObjectKey); err != nil {
							t.Fatalf("completion retry is not idempotent: %v", err)
						}
					}
					for _, repo := range []HostedRepository{f.repo, target} {
						if err := conanCollectionRestore(ctx, store, f.session, repo.ID); !errors.Is(err, ErrNotFound) {
							t.Fatalf("restore crossed collector boundary completed=%t repo=%s: %v", completed, repo.ID, err)
						}
						conanCollectionAssertDeleted(t, store, f.session, repo.ID)
					}
					if err := store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: f.repo.ID, ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}); err == nil {
						t.Fatalf("restaging fenced object succeeded completed=%t", completed)
					}
					if kind == "recipe" {
						if _, err := store.PutConanRecipeRevision(ctx, ConanRecipeRevision{RepositoryID: f.repo.ID, Reference: f.session.Reference, Revision: f.session.RecipeRevision, Digest: f.digest()}, assets); !errors.Is(err, ErrDisabled) {
							t.Fatalf("recipe put crossed collection boundary: %v", err)
						}
					} else if _, err := store.PutConanPackageRevision(ctx, ConanPackageRevision{RepositoryID: f.repo.ID, Reference: f.session.Reference, RecipeRevision: f.session.RecipeRevision, PackageID: f.session.PackageID, Revision: f.session.PackageRevision, Digest: f.digest()}, assets); !errors.Is(err, ErrDisabled) {
						t.Fatalf("package put crossed collection boundary: %v", err)
					}
					if visible, err := store.ConanObjectHasVisibleReference(ctx, asset.ObjectKey); err != nil || visible {
						t.Fatalf("fenced object became visible: visible=%t err=%v", visible, err)
					}
					f.assertSession(t)
					if audits := f.publicationAudits(t); len(audits) != 1 {
						t.Fatalf("failed writers changed original success audit: %+v", audits)
					}
				}
				if collect, err := store.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || collect {
					t.Fatalf("already collected object was reclaimed: collect=%t err=%v", collect, err)
				}
			})
			t.Run("snapshot_reference_wins_after_candidate_listing", func(t *testing.T) {
				f := newConanManagedContractFixture(t, store, kind)
				declared := f.session.Objects[0]
				key := f.intent(t, declared)
				candidates, err := store.ListReclaimableConanObjects(ctx, time.Now().Add(time.Hour), 100000)
				if err != nil || !conanCollectionContains(candidates, key) {
					t.Fatalf("candidate was not initially reclaimable: err=%v", err)
				}
				next, err := store.StageConanPublicationObject(ctx, f.session.ID, declared, key)
				if err != nil {
					t.Fatal(err)
				}
				f.session, f.uploads[declared.Name] = next, key
				if collect, err := store.BeginConanObjectCollection(ctx, key); err != nil || collect {
					t.Fatalf("candidate listing bypassed new open snapshot reference: collect=%t err=%v", collect, err)
				}
				for _, declared := range f.session.Objects[1:] {
					f.stage(t, declared)
				}
				f.commit(t, "reference-winner")
			})
			t.Run("replaced_snapshot_cannot_rebind_fenced_key", func(t *testing.T) {
				f := newConanManagedContractFixture(t, store, kind)
				f.stageAll(t)
				declared, staleVersion := f.session.Objects[0], f.session.Version
				oldKey := f.uploads[declared.Name]
				f.stage(t, declared)
				if collect, err := store.BeginConanObjectCollection(ctx, oldKey); err != nil || !collect {
					t.Fatalf("replaced object still referenced: collect=%t err=%v", collect, err)
				}
				for _, completed := range []bool{false, true} {
					if completed {
						if err := store.CompleteConanObjectCollection(ctx, oldKey); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := store.StageConanPublicationObject(ctx, f.session.ID, declared, oldKey); !errors.Is(err, ErrDisabled) {
						t.Fatalf("ABA rebind accepted completed=%t: %v", completed, err)
					}
					f.assertUnpublished(t)
					if _, _, err := store.CommitConanPublicationSession(ctx, f.session.ID, staleVersion, f.digest(), "stale"); !errors.Is(err, ErrVersionConflict) {
						t.Fatalf("old snapshot crossed replacement: %v", err)
					}
				}
				f.commit(t, "replacement-winner")
			})
		})
	}
	t.Run("replication_cannot_claim_collecting_or_collected_target", func(t *testing.T) {
		for _, completed := range []bool{false, true} {
			f := newConanManagedContractFixture(t, store, "recipe")
			f.stageAll(t)
			f.commit(t, "source")
			source, err := store.GetConanRecipeRevision(ctx, f.repo.ID, f.session.Reference, f.session.RecipeRevision)
			if err != nil {
				t.Fatal(err)
			}
			target := conanCollectionRepository(t, store)
			targetRecipe := source
			targetRecipe.RepositoryID = target.ID
			sourceAssets := f.assets(t)
			targetAssets := append([]ConanAsset(nil), sourceAssets...)
			for i := range targetAssets {
				targetAssets[i].RepositoryID, targetAssets[i].ObjectKey = target.ID, "native/conan/collection/"+uuid.NewString()
			}
			blocked := targetAssets[0]
			if err := store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: target.ID, ObjectKey: blocked.ObjectKey, Digest: blocked.Digest, Size: blocked.Size}); err != nil {
				t.Fatal(err)
			}
			if collect, err := store.BeginConanObjectCollection(ctx, blocked.ObjectKey); err != nil || !collect {
				t.Fatalf("claim replication target collect=%t err=%v", collect, err)
			}
			if completed {
				if err := store.CompleteConanObjectCollection(ctx, blocked.ObjectKey); err != nil {
					t.Fatal(err)
				}
			}
			before := object(t, blocked.ObjectKey)
			_, err = store.PublishReplicatedConanRevision(ctx, ConanReplicationPublication{SourceRepositoryID: f.repo.ID, Recipe: targetRecipe, SourceAssets: sourceAssets, TargetAssets: targetAssets})
			if !errors.Is(err, ErrDisabled) {
				t.Fatalf("replication crossed target collection completed=%t: %v", completed, err)
			}
			if _, err := store.GetConanRecipeRevision(ctx, target.ID, source.Reference, source.Revision); !errors.Is(err, ErrNotFound) {
				t.Fatalf("failed replication exposed target metadata: %v", err)
			}
			if after := object(t, blocked.ObjectKey); !reflect.DeepEqual(before, after) {
				t.Fatalf("failed replication rebound fenced intent: before=%+v after=%+v", before, after)
			}
		}
	})
	t.Run("promotion_excludes_collected_historical_package", func(t *testing.T) {
		f, previousTarget := newConanCollectionPromotion(t, store, "package")
		conanCollectionTombstone(t, store, f.session, f.repo.ID)
		conanCollectionTombstone(t, store, f.session, previousTarget.ID)
		for _, asset := range f.assets(t) {
			if collect, err := store.BeginConanObjectCollection(ctx, asset.ObjectKey); err != nil || !collect {
				t.Fatalf("claim historical package: collect=%t err=%v", collect, err)
			}
			if err := store.CompleteConanObjectCollection(ctx, asset.ObjectKey); err != nil {
				t.Fatal(err)
			}
		}
		target := conanCollectionRepository(t, store)
		recipe, err := store.GetConanRecipeRevision(ctx, f.repo.ID, f.session.Reference, f.session.RecipeRevision)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.PromoteConanRecipeRevision(ctx, ConanPromotion{SourceRepositoryID: f.repo.ID, TargetRepositoryID: target.ID, Reference: recipe.Reference, Revision: recipe.Revision, Digest: recipe.Digest}); err != nil {
			t.Fatalf("collected historical package blocked visible recipe promotion: %v", err)
		}
		assets, err := store.ListConanPackageAssets(ctx, target.ID, f.session.Reference, f.session.RecipeRevision, f.session.PackageID, f.session.PackageRevision)
		if err != nil || len(assets) != 0 {
			t.Fatalf("promotion copied collected historical assets: %+v err=%v", assets, err)
		}
		if _, err := store.GetConanPackageRevision(ctx, target.ID, f.session.Reference, f.session.RecipeRevision, f.session.PackageID, f.session.PackageRevision); !errors.Is(err, ErrNotFound) {
			t.Fatalf("promotion exposed collected historical package: %v", err)
		}
	})
}

func conanCollectionRepository(t *testing.T, store HostedRepositoryStore) HostedRepository {
	t.Helper()
	repo, err := store.CreateHostedRepository(context.Background(), HostedRepository{ID: uuid.NewString(), Name: "conan-collection-" + uuid.NewString(), Format: FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func newConanCollectionPromotion(t *testing.T, store conanCollectionContractStore, kind string) (*conanManagedContractFixture, HostedRepository) {
	t.Helper()
	f := newConanManagedContractFixture(t, store, kind)
	f.stageAll(t)
	f.commit(t, "source")
	target := conanCollectionRepository(t, store)
	recipe, err := store.GetConanRecipeRevision(context.Background(), f.repo.ID, f.session.Reference, f.session.RecipeRevision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PromoteConanRecipeRevision(context.Background(), ConanPromotion{SourceRepositoryID: f.repo.ID, TargetRepositoryID: target.ID, Reference: recipe.Reference, Revision: recipe.Revision, Digest: recipe.Digest}); err != nil {
		t.Fatal(err)
	}
	return f, target
}

func conanCollectionTombstone(t *testing.T, store NativeConanStore, session ConanPublishSession, repo string) {
	t.Helper()
	var err error
	if session.Kind == "recipe" {
		_, err = store.TombstoneConanRecipeRevision(context.Background(), repo, session.Reference, session.RecipeRevision)
	} else {
		_, err = store.TombstoneConanPackageRevision(context.Background(), repo, session.Reference, session.RecipeRevision, session.PackageID, session.PackageRevision)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func conanCollectionRestore(ctx context.Context, store NativeConanStore, session ConanPublishSession, repo string) error {
	if session.Kind == "recipe" {
		_, err := store.RestoreConanRecipeRevision(ctx, repo, session.Reference, session.RecipeRevision)
		return err
	}
	_, err := store.RestoreConanPackageRevision(ctx, repo, session.Reference, session.RecipeRevision, session.PackageID, session.PackageRevision)
	return err
}

func conanCollectionAssertDeleted(t *testing.T, store NativeConanStore, session ConanPublishSession, repo string) {
	t.Helper()
	var state string
	var err error
	if session.Kind == "recipe" {
		var revision ConanRecipeRevision
		revision, err = store.GetConanRecipeRevision(context.Background(), repo, session.Reference, session.RecipeRevision)
		state = revision.State
	} else {
		var revision ConanPackageRevision
		revision, err = store.GetConanPackageRevision(context.Background(), repo, session.Reference, session.RecipeRevision, session.PackageID, session.PackageRevision)
		state = revision.State
	}
	if err != nil || state != "deleted" {
		t.Fatalf("failed restore changed revision: state=%s err=%v", state, err)
	}
}

func conanCollectionContains(objects []ConanObjectIntent, key string) bool {
	for _, object := range objects {
		if object.ObjectKey == key {
			return true
		}
	}
	return false
}
