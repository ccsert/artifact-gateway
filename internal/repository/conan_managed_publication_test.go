package repository

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type conanManagedPublicationContractStore interface {
	conanPublicationContractStore
	AuditStore
}

func TestMemoryConanManagedPublication(t *testing.T) {
	store := NewMemoryStore()
	exerciseConanManagedPublication(t, store, func(t *testing.T, id string) {
		t.Helper()
		store.mu.Lock()
		defer store.mu.Unlock()
		session := store.conanSessions[id]
		session.ExpiresAt = time.Now().Add(-time.Hour)
		store.conanSessions[id] = session
	})
}

func exerciseConanManagedPublication(t *testing.T, store conanManagedPublicationContractStore, expire func(*testing.T, string)) {
	t.Helper()
	ctx := context.Background()
	for _, kind := range []string{"recipe", "package"} {
		t.Run(kind, func(t *testing.T) {
			t.Run("requires_every_declared_object", func(t *testing.T) {
				for _, missing := range conanManagedContractObjects(kind) {
					t.Run(missing.Name, func(t *testing.T) {
						f := newConanManagedContractFixture(t, store, kind)
						for _, object := range f.session.Objects {
							if object.Name != missing.Name {
								f.stage(t, object)
							}
						}
						if _, _, err := store.CommitConanPublicationSession(ctx, f.session.ID, f.session.Version, f.digest(), "committer"); !errors.Is(err, ErrDisabled) {
							t.Fatalf("publication missing %s committed: %v", missing.Name, err)
						}
						f.assertUnpublished(t)
						f.assertGCEligibility(t, false)
					})
				}
			})
			t.Run("declaration_is_fixed", func(t *testing.T) {
				for _, change := range []string{"digest", "size", "unknown_name"} {
					t.Run(change, func(t *testing.T) {
						f := newConanManagedContractFixture(t, store, kind)
						f.stage(t, f.session.Objects[0])
						changed := f.session.Objects[0]
						switch change {
						case "digest":
							changed.Digest = "sha256:" + strings.Repeat("f", 64)
						case "size":
							changed.Size++
						case "unknown_name":
							changed.Name = "undeclared.tgz"
						}
						// Match the new intent to the attempted object, so rejection
						// proves the fixed declaration gate rather than an intent mismatch.
						key := f.intent(t, changed)
						if _, err := store.StageConanPublicationObject(ctx, f.session.ID, changed, key); !errors.Is(err, ErrDisabled) {
							t.Fatalf("changed %s accepted: %v", change, err)
						}
						f.assertUnpublished(t)
					})
				}
			})
			t.Run("stale_complete_snapshot_is_atomic", func(t *testing.T) {
				f := newConanManagedContractFixture(t, store, kind)
				f.stageAll(t)
				staleVersion := f.session.Version
				object := f.session.Objects[0]
				oldKey := f.uploads[object.Name]
				f.stage(t, object)
				if f.uploads[object.Name] == oldKey {
					t.Fatal("replacement upload reused its mutable object key")
				}
				if _, _, err := store.CommitConanPublicationSession(ctx, f.session.ID, staleVersion, f.digest(), "stale-committer"); !errors.Is(err, ErrVersionConflict) {
					t.Fatalf("stale complete snapshot committed: %v", err)
				}
				f.assertUnpublished(t)
				f.assertGCEligibility(t, false)
				f.commit(t, "actual-committer")
			})
			t.Run("quota_failure_is_atomic", func(t *testing.T) {
				f := newConanManagedContractFixture(t, store, kind)
				f.stageAll(t)
				if _, err := store.ReplaceRepositoryCapacityQuota(ctx, f.repo.ID, f.usedBefore+f.declaredBytes()-1); err != nil {
					t.Fatal(err)
				}
				if _, _, err := store.CommitConanPublicationSession(ctx, f.session.ID, f.session.Version, f.digest(), "quota-committer"); !IsQuotaExceeded(err) {
					t.Fatalf("over-quota publication=%v", err)
				}
				f.assertUnpublished(t)
				f.assertGCEligibility(t, false)
				if _, err := store.ReplaceRepositoryCapacityQuota(ctx, f.repo.ID, 0); err != nil {
					t.Fatal(err)
				}
				f.commit(t, "actual-committer")
			})
			t.Run("expiry_fences_writer_and_releases_pending_objects", func(t *testing.T) {
				f := newConanManagedContractFixture(t, store, kind)
				f.stageAll(t)
				f.assertGCEligibility(t, false)
				expire(t, f.session.ID)
				if _, _, err := store.CommitConanPublicationSession(ctx, f.session.ID, f.session.Version, f.digest(), "expired-committer"); !errors.Is(err, ErrVersionConflict) {
					t.Fatalf("expired publication committed: %v", err)
				}
				f.assertUnpublished(t)
				f.assertGCEligibility(t, true)
			})
		})
	}
}

type conanManagedContractFixture struct {
	store        conanManagedPublicationContractStore
	repo         HostedRepository
	session      ConanPublishSession
	uploads      map[string]string
	usedBefore   int64
	auditsBefore int
	parentRecipe *ConanRecipeRevision
}

func newConanManagedContractFixture(t *testing.T, store conanManagedPublicationContractStore, kind string) *conanManagedContractFixture {
	t.Helper()
	repo, err := store.CreateHostedRepository(context.Background(), HostedRepository{ID: uuid.NewString(), Name: "conan-managed-" + uuid.NewString(), Format: FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if kind == "package" {
		recipe := newConanManagedContractSession(t, store, repo, "recipe")
		recipe.stageAll(t)
		recipe.commit(t, "recipe-committer")
	}
	return newConanManagedContractSession(t, store, repo, kind)
}

func newConanManagedContractSession(t *testing.T, store conanManagedPublicationContractStore, repo HostedRepository, kind string) *conanManagedContractFixture {
	t.Helper()
	ctx := context.Background()
	incoming := ConanPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Publisher: "session-creator", Kind: kind, Reference: "pkg/1.0/_/_", RecipeRevision: strings.Repeat("a", 32), State: "open", ExpiresAt: time.Now().UTC().Add(time.Hour), Objects: conanManagedContractObjects(kind), Native: true, Version: 99}
	if kind == "package" {
		incoming.PackageID, incoming.PackageRevision = strings.Repeat("b", 40), strings.Repeat("c", 32)
	}
	session, err := store.CreateConanPublishSession(ctx, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if session.Native || session.Version != 1 || session.State != "open" || session.Publisher != incoming.Publisher || !reflect.DeepEqual(session.Objects, incoming.Objects) {
		t.Fatalf("management create did not fix its session mode and version: %+v", session)
	}
	capacity, err := store.GetRepositoryCapacity(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	audits, err := store.ListAudits(ctx, AuditQuery{Repository: repo.Name, Operation: "publish", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	f := &conanManagedContractFixture{store: store, repo: repo, session: session, uploads: make(map[string]string), usedBefore: capacity.UsedBytes, auditsBefore: len(audits)}
	if kind == "package" {
		recipe, err := store.GetConanRecipeRevision(ctx, repo.ID, session.Reference, session.RecipeRevision)
		if err != nil {
			t.Fatal(err)
		}
		f.parentRecipe = &recipe
	}
	return f
}

func conanManagedContractObjects(kind string) []MavenDeclaredObject {
	names := []string{"conanfile.py", "conanmanifest.txt"}
	if kind == "package" {
		names = []string{"conaninfo.txt", "conan_package.tgz", "conanmanifest.txt"}
	}
	objects := make([]MavenDeclaredObject, 0, len(names))
	for _, name := range names {
		metadata := []byte(kind + ":" + name)
		objects = append(objects, MavenDeclaredObject{Name: name, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(metadata)), Size: int64(len(metadata))})
	}
	return objects
}

func (f *conanManagedContractFixture) intent(t *testing.T, object MavenDeclaredObject) string {
	t.Helper()
	key := "native/conan/sessions/" + f.session.ID + "/" + uuid.NewString() + "/" + object.Name
	if err := f.store.StageConanObject(context.Background(), ConanObjectIntent{RepositoryID: f.repo.ID, ObjectKey: key, Digest: object.Digest, Size: object.Size}); err != nil {
		t.Fatal(err)
	}
	return key
}

func (f *conanManagedContractFixture) stage(t *testing.T, object MavenDeclaredObject) {
	t.Helper()
	key := f.intent(t, object)
	next, err := f.store.StageConanPublicationObject(context.Background(), f.session.ID, object, key)
	if err != nil {
		t.Fatal(err)
	}
	if next.Native || next.Version != f.session.Version+1 || next.Publisher != f.session.Publisher || !reflect.DeepEqual(next.Objects, f.session.Objects) {
		t.Fatalf("stage changed fixed declaration or failed to advance its version: before=%+v after=%+v", f.session, next)
	}
	f.session = next
	f.uploads[object.Name] = key
}

func (f *conanManagedContractFixture) stageAll(t *testing.T) {
	t.Helper()
	for _, object := range f.session.Objects {
		f.stage(t, object)
	}
}

func (f *conanManagedContractFixture) digest() string {
	objects := append([]MavenDeclaredObject(nil), f.session.Objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].Name < objects[j].Name })
	hash := sha256.New()
	for _, object := range objects {
		_, _ = hash.Write([]byte(object.Name + "\x00" + object.Digest + "\x00"))
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

func (f *conanManagedContractFixture) declaredBytes() int64 {
	var size int64
	for _, object := range f.session.Objects {
		size += object.Size
	}
	return size
}

func (f *conanManagedContractFixture) publicationAudits(t *testing.T) []AuditRecord {
	t.Helper()
	audits, err := f.store.ListAudits(context.Background(), AuditQuery{Repository: f.repo.Name, Operation: "publish", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var found []AuditRecord
	for _, audit := range audits {
		if audit.Evidence["publicationSessionId"] == f.session.ID {
			found = append(found, audit)
		}
	}
	if len(audits) != f.auditsBefore+len(found) {
		t.Fatalf("publication changed earlier audits or wrote an audit without its session identity: %+v", audits)
	}
	return found
}

func (f *conanManagedContractFixture) assertSession(t *testing.T) {
	t.Helper()
	current, err := f.store.GetConanPublishSession(context.Background(), f.session.ID)
	if err != nil || current.State != f.session.State || current.Version != f.session.Version || current.Native || current.Publisher != "session-creator" || current.RepositoryID != f.repo.ID || current.Kind != f.session.Kind || current.Reference != f.session.Reference || current.RecipeRevision != f.session.RecipeRevision || current.PackageID != f.session.PackageID || current.PackageRevision != f.session.PackageRevision || !reflect.DeepEqual(current.Objects, f.session.Objects) {
		t.Fatalf("publication changed session snapshot: current=%+v expected=%+v err=%v", current, f.session, err)
	}
	uploads, err := f.store.ListConanPublishUploads(context.Background(), f.session.ID)
	if err != nil || !reflect.DeepEqual(uploads, f.uploads) {
		t.Fatalf("publication changed uploaded pointers: current=%v expected=%v err=%v", uploads, f.uploads, err)
	}
}

func (f *conanManagedContractFixture) assets(t *testing.T) []ConanAsset {
	t.Helper()
	var assets []ConanAsset
	var err error
	if f.session.Kind == "recipe" {
		assets, err = f.store.ListConanRecipeAssets(context.Background(), f.repo.ID, f.session.Reference, f.session.RecipeRevision)
	} else {
		assets, err = f.store.ListConanPackageAssets(context.Background(), f.repo.ID, f.session.Reference, f.session.RecipeRevision, f.session.PackageID, f.session.PackageRevision)
	}
	if err != nil {
		t.Fatal(err)
	}
	return assets
}

func (f *conanManagedContractFixture) assertUnpublished(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	f.assertSession(t)
	var err error
	if f.session.Kind == "recipe" {
		_, err = f.store.GetConanRecipeRevision(ctx, f.repo.ID, f.session.Reference, f.session.RecipeRevision)
	} else {
		_, err = f.store.GetConanPackageRevision(ctx, f.repo.ID, f.session.Reference, f.session.RecipeRevision, f.session.PackageID, f.session.PackageRevision)
		recipe, recipeErr := f.store.GetConanRecipeRevision(ctx, f.repo.ID, f.session.Reference, f.session.RecipeRevision)
		if recipeErr != nil || f.parentRecipe == nil || !reflect.DeepEqual(recipe, *f.parentRecipe) {
			t.Fatalf("failed package publication changed its parent recipe: %+v err=%v", recipe, recipeErr)
		}
	}
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed publication exposed revision: %v", err)
	}
	if assets := f.assets(t); len(assets) != 0 {
		t.Fatalf("failed publication exposed assets: %+v", assets)
	}
	if audits := f.publicationAudits(t); len(audits) != 0 {
		t.Fatalf("failed publication persisted success audit: %+v", audits)
	}
	capacity, err := f.store.GetRepositoryCapacity(ctx, f.repo.ID)
	if err != nil || capacity.UsedBytes != f.usedBefore {
		t.Fatalf("failed publication claimed capacity: %+v err=%v", capacity, err)
	}
	for _, key := range f.uploads {
		if visible, err := f.store.ConanObjectHasVisibleReference(ctx, key); err != nil || visible {
			t.Fatalf("failed publication object became visible: %s visible=%t err=%v", key, visible, err)
		}
	}
}

func (f *conanManagedContractFixture) commit(t *testing.T, actor string) {
	t.Helper()
	ctx := context.Background()
	committed, replay, err := f.store.CommitConanPublicationSession(ctx, f.session.ID, f.session.Version, f.digest(), actor)
	if err != nil || replay || committed.State != "committed" || committed.Publisher != "session-creator" || committed.Version != f.session.Version || !reflect.DeepEqual(committed.Objects, f.session.Objects) {
		t.Fatalf("complete managed commit=%+v replay=%t err=%v", committed, replay, err)
	}
	f.session = committed
	f.assertSession(t)
	var digest, state string
	if f.session.Kind == "recipe" {
		revision, err := f.store.GetConanRecipeRevision(ctx, f.repo.ID, f.session.Reference, f.session.RecipeRevision)
		if err != nil {
			t.Fatal(err)
		}
		digest, state = revision.Digest, revision.State
	} else {
		revision, err := f.store.GetConanPackageRevision(ctx, f.repo.ID, f.session.Reference, f.session.RecipeRevision, f.session.PackageID, f.session.PackageRevision)
		if err != nil {
			t.Fatal(err)
		}
		digest, state = revision.Digest, revision.State
	}
	if digest != f.digest() || state != "visible" {
		t.Fatalf("committed revision digest=%s state=%s", digest, state)
	}
	assets := f.assets(t)
	if len(assets) != len(f.session.Objects) {
		t.Fatalf("committed assets=%+v", assets)
	}
	byName := make(map[string]ConanAsset, len(assets))
	for _, asset := range assets {
		byName[asset.Path] = asset
	}
	for _, object := range f.session.Objects {
		asset := byName[object.Name]
		if asset.RepositoryID != f.repo.ID || asset.Reference != f.session.Reference || asset.RecipeRevision != f.session.RecipeRevision || asset.PackageID != f.session.PackageID || asset.PackageRevision != f.session.PackageRevision || asset.ObjectKey != f.uploads[object.Name] || asset.Digest != object.Digest || asset.Size != object.Size {
			t.Fatalf("committed asset mismatches snapshot: %+v object=%+v", asset, object)
		}
		if visible, err := f.store.ConanObjectHasVisibleReference(ctx, asset.ObjectKey); err != nil || !visible {
			t.Fatalf("committed object is invisible: %s visible=%t err=%v", asset.ObjectKey, visible, err)
		}
	}
	capacity, err := f.store.GetRepositoryCapacity(ctx, f.repo.ID)
	if err != nil || capacity.UsedBytes != f.usedBefore+f.declaredBytes() {
		t.Fatalf("committed capacity=%+v err=%v", capacity, err)
	}
	audits := f.publicationAudits(t)
	if len(audits) != 1 {
		t.Fatalf("managed commit audits=%+v, want exactly one", audits)
	}
	audit := audits[0]
	coordinate := f.session.Reference + "#" + f.session.RecipeRevision
	if f.session.Kind == "package" {
		coordinate += "/" + f.session.PackageID + "#" + f.session.PackageRevision
	}
	if audit.Actor != actor || audit.Repository != f.repo.Name || audit.GroupName != f.repo.Name || audit.Resource != coordinate || audit.Operation != "publish" || audit.Format != "conan" || audit.MemberName != "management" || audit.MemberType != "hosted" || audit.Status != 200 || audit.Outcome != AuditResolved || audit.Evidence["publicationState"] != "committed" || audit.Evidence["digest"] != f.digest() || audit.Evidence["replay"] != "false" {
		t.Fatalf("managed audit did not record actual committer and snapshot: %+v", audit)
	}
	f.assertGCEligibility(t, false)
}

func (f *conanManagedContractFixture) assertGCEligibility(t *testing.T, eligible bool) {
	t.Helper()
	objects, err := f.store.ListReclaimableConanObjects(context.Background(), time.Now().Add(48*time.Hour), 10000)
	if err != nil {
		t.Fatal(err)
	}
	candidates := make(map[string]ConanObjectIntent, len(objects))
	for _, object := range objects {
		candidates[object.ObjectKey] = object
	}
	for _, declared := range f.session.Objects {
		key, staged := f.uploads[declared.Name]
		if !staged {
			continue
		}
		object, found := candidates[key]
		if found != eligible {
			t.Fatalf("GC eligibility for %s=%t, want %t", key, found, eligible)
		}
		if found && (object.RepositoryID != f.repo.ID || object.Digest != declared.Digest || object.Size != declared.Size || !object.ClaimedAt.IsZero() || !object.CollectedAt.IsZero()) {
			t.Fatalf("expired publication object metadata=%+v, declaration=%+v", object, declared)
		}
	}
}
