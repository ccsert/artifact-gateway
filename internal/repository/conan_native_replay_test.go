package repository

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMemoryConanNativeReplaySnapshot(t *testing.T) {
	store := NewMemoryStore()
	exerciseConanNativeReplaySnapshot(t, store, func(t *testing.T, id string) {
		t.Helper()
		store.mu.Lock()
		defer store.mu.Unlock()
		session := store.conanSessions[id]
		session.ExpiresAt = session.ExpiresAt.Add(-2 * time.Hour)
		store.conanSessions[id] = session
	}, func(t *testing.T, key string, collecting bool) {
		t.Helper()
		store.mu.Lock()
		defer store.mu.Unlock()
		intent := store.conanObjects[key]
		if collecting {
			intent.CollectingAt = time.Now().UTC()
		} else {
			intent.CollectedAt = time.Now().UTC()
		}
		store.conanObjects[key] = intent
	})
}

func exerciseConanNativeReplaySnapshot(t *testing.T, store conanManagedPublicationContractStore, expire func(*testing.T, string), unavailable func(*testing.T, string, bool)) {
	t.Helper()
	ctx := context.Background()
	for _, kind := range []string{"recipe", "package"} {
		t.Run(kind, func(t *testing.T) {
			t.Run("expired_commit_and_other_actor", func(t *testing.T) {
				f := newConanManagedContractFixture(t, store, kind)
				f.stageAll(t)
				f.commit(t, "first-committer")
				expire(t, f.session.ID)
				incoming := f.session
				incoming.Publisher = "retrying-actor"
				session, err := store.BeginConanNativeSession(ctx, incoming)
				if err != nil {
					t.Fatal(err)
				}
				assertConanReplaySnapshot(t, store, session, f.session.Objects, f.uploads)
				if session.ID == f.session.ID || session.Publisher != incoming.Publisher || !session.Native || session.Version != 1 || session.State != "open" {
					t.Fatalf("retry did not create its own open snapshot: %+v", session)
				}
				// Returned slices must not mutate the durable replay snapshot.
				session.Objects[0].Digest = "caller mutation"
				session, err = store.GetConanPublishSession(ctx, session.ID)
				if err != nil {
					t.Fatal(err)
				}
				assertConanReplaySnapshot(t, store, session, f.session.Objects, f.uploads)
				object := f.session.Objects[0]
				// Reusing a claimed key through Stage remains forbidden. Only the
				// store's atomic snapshot may retain an already published key.
				if _, err = store.StageConanPublicationObject(ctx, session.ID, object, f.uploads[object.Name]); !errors.Is(err, ErrDisabled) {
					t.Fatalf("published intent admitted as a new upload: %v", err)
				}
				before, err := store.GetRepositoryCapacity(ctx, f.repo.ID)
				if err != nil {
					t.Fatal(err)
				}
				committed, replay, err := store.CommitConanPublicationSession(ctx, session.ID, session.Version, f.digest(), incoming.Publisher)
				if err != nil || !replay || committed.State != "committed" {
					t.Fatalf("snapshot replay=%+v replay=%t err=%v", committed, replay, err)
				}
				after, err := store.GetRepositoryCapacity(ctx, f.repo.ID)
				if err != nil || after.UsedBytes != before.UsedBytes {
					t.Fatalf("replay changed capacity: before=%+v after=%+v err=%v", before, after, err)
				}
				audits, err := store.ListAudits(ctx, AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
				if err != nil {
					t.Fatal(err)
				}
				found := 0
				for _, audit := range audits {
					if audit.Evidence["publicationSessionId"] == session.ID {
						found++
						if audit.Actor != incoming.Publisher || audit.Evidence["replay"] != "true" || audit.Evidence["publicationState"] != "committed" {
							t.Errorf("retry audit lost its actor or replay identity: %+v", audit)
						}
					}
				}
				if found != 1 {
					t.Fatalf("retry successful audits=%d", found)
				}
			})
			t.Run("existing_open_upload_is_never_overwritten", func(t *testing.T) {
				f := newConanManagedContractFixture(t, store, kind)
				incoming := f.session
				incoming.Publisher = "native-writer"
				pending, err := store.BeginConanNativeSession(ctx, incoming)
				if err != nil || len(pending.Objects) != 0 {
					t.Fatalf("unpublished revision unexpectedly seeded: %+v %v", pending, err)
				}
				bad := f.session.Objects[0]
				bad.Digest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
				key := f.intent(t, bad)
				pending, err = store.StageConanPublicationObject(ctx, pending.ID, bad, key)
				if err != nil {
					t.Fatal(err)
				}
				f.stageAll(t)
				f.commit(t, "another-writer")
				again, err := store.BeginConanNativeSession(ctx, incoming)
				if err != nil || !reflect.DeepEqual(again, pending) {
					t.Fatalf("begin replaced a live writer's snapshot: before=%+v after=%+v err=%v", pending, again, err)
				}
				assertConanReplaySnapshot(t, store, again, []MavenDeclaredObject{bad}, map[string]string{bad.Name: key})
				expire(t, pending.ID)
				fresh, err := store.BeginConanNativeSession(ctx, incoming)
				if err != nil || fresh.ID == pending.ID {
					t.Fatalf("expired writer reused: %+v %v", fresh, err)
				}
				assertConanReplaySnapshot(t, store, fresh, f.session.Objects, f.uploads)
				if _, _, err = store.CommitConanPublicationSession(ctx, pending.ID, pending.Version, f.digest(), incoming.Publisher); !errors.Is(err, ErrVersionConflict) {
					t.Fatalf("expired pending writer committed: %v", err)
				}
			})
			for _, state := range []string{"collecting", "collected"} {
				t.Run(state+"_objects_cannot_replay", func(t *testing.T) {
					f := newConanManagedContractFixture(t, store, kind)
					f.stageAll(t)
					f.commit(t, "first-committer")
					session, err := store.BeginConanNativeSession(ctx, f.session)
					if err != nil {
						t.Fatal(err)
					}
					assertConanReplaySnapshot(t, store, session, f.session.Objects, f.uploads)
					// Inject an unavailable persisted intent to prove both the
					// fresh snapshot and final commit fail closed on damaged state.
					unavailable(t, f.uploads[f.session.Objects[0].Name], state == "collecting")
					if _, _, err = store.CommitConanPublicationSession(ctx, session.ID, session.Version, f.digest(), "retrying-actor"); !errors.Is(err, ErrDisabled) {
						t.Fatalf("unavailable snapshot replayed: %v", err)
					}
					incoming := f.session
					incoming.Publisher = "another-retry"
					if _, err = store.BeginConanNativeSession(ctx, incoming); !errors.Is(err, ErrDisabled) {
						t.Fatalf("unavailable snapshot seeded: %v", err)
					}
				})
			}
			t.Run("tombstoned_revision_is_not_a_seed", func(t *testing.T) {
				f := newConanManagedContractFixture(t, store, kind)
				f.stageAll(t)
				f.commit(t, "first-committer")
				if _, err := store.TombstoneConanRecipeRevision(ctx, f.repo.ID, f.session.Reference, f.session.RecipeRevision); err != nil {
					t.Fatal(err)
				}
				session, err := store.BeginConanNativeSession(ctx, f.session)
				if err != nil || len(session.Objects) != 0 {
					t.Fatalf("tombstoned revision seeded a retry: %+v %v", session, err)
				}
			})
			t.Run("promoted_target_retains_only_its_exact_shared_assets", func(t *testing.T) {
				f := newConanManagedContractFixture(t, store, kind)
				f.stageAll(t)
				f.commit(t, "first-committer")
				target, err := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "conan-replay-" + uuid.NewString(), Format: FormatConan})
				if err != nil {
					t.Fatal(err)
				}
				recipe, err := store.GetConanRecipeRevision(ctx, f.repo.ID, f.session.Reference, f.session.RecipeRevision)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = store.PromoteConanRecipeRevision(ctx, ConanPromotion{SourceRepositoryID: f.repo.ID, TargetRepositoryID: target.ID, Reference: recipe.Reference, Revision: recipe.Revision, Digest: recipe.Digest}); err != nil {
					t.Fatal(err)
				}
				incoming := f.session
				incoming.RepositoryID, incoming.Publisher = target.ID, "target-writer"
				session, err := store.BeginConanNativeSession(ctx, incoming)
				if err != nil {
					t.Fatal(err)
				}
				assertConanReplaySnapshot(t, store, session, f.session.Objects, f.uploads)
				// A shared source key is not an eligible new staged upload.
				object := f.session.Objects[0]
				if _, err = store.StageConanPublicationObject(ctx, session.ID, object, f.uploads[object.Name]); !errors.Is(err, ErrDisabled) {
					t.Fatalf("cross-repository key admitted as a new upload: %v", err)
				}
				committed, replay, err := store.CommitConanPublicationSession(ctx, session.ID, session.Version, f.digest(), incoming.Publisher)
				if err != nil || !replay || committed.State != "committed" {
					t.Fatalf("target's exact published shared tree cannot replay: %+v %t %v", committed, replay, err)
				}
			})
		})
	}
}

func assertConanReplaySnapshot(t *testing.T, store conanPublicationContractStore, session ConanPublishSession, objects []MavenDeclaredObject, uploads map[string]string) {
	t.Helper()
	got := append([]MavenDeclaredObject(nil), session.Objects...)
	want := append([]MavenDeclaredObject(nil), objects...)
	sort.Slice(got, func(i, j int) bool { return got[i].Name < got[j].Name })
	sort.Slice(want, func(i, j int) bool { return want[i].Name < want[j].Name })
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retry does not retain the complete exact published snapshot: got=%+v want=%+v", got, want)
	}
	actual, err := store.ListConanPublishUploads(context.Background(), session.ID)
	if err != nil || !reflect.DeepEqual(actual, uploads) {
		t.Fatalf("retry pointers=%+v want=%+v err=%v", actual, uploads, err)
	}
}
