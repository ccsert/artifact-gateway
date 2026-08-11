package conan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativePromotionWorkerBlocksRecipeAndPackageQuarantinedAfterEnqueue(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	reference, revision := "quarantined/1.0/user/stable", "rrev"
	recipeDigest := "sha256:" + strings.Repeat("a", 64)
	packageDigest := "sha256:" + strings.Repeat("b", 64)
	recipeKey, packageKey := "native/conan/source/quarantined-recipe", "native/conan/source/quarantined-package"
	for _, item := range []struct {
		key    string
		digest string
		size   int64
	}{{recipeKey, recipeDigest, 6}, {packageKey, packageDigest, 7}} {
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{
			RepositoryID: "source", ObjectKey: item.key, Digest: item.digest, Size: item.size,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{
		RepositoryID: "source", Reference: reference, Revision: revision, Digest: recipeDigest,
	}, []repository.ConanAsset{{
		RepositoryID: "source", Reference: reference, RecipeRevision: revision,
		Path: "conanfile.py", ObjectKey: recipeKey, Digest: recipeDigest, Size: 6,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanPackageRevision(ctx, repository.ConanPackageRevision{
		RepositoryID: "source", Reference: reference, RecipeRevision: revision,
		PackageID: "package-id", Revision: "prev", Digest: packageDigest,
	}, []repository.ConanAsset{{
		RepositoryID: "source", Reference: reference, RecipeRevision: revision,
		PackageID: "package-id", PackageRevision: "prev", Path: "package.tgz",
		ObjectKey: packageKey, Digest: packageDigest, Size: 7,
	}}); err != nil {
		t.Fatal(err)
	}

	worker := NativePromotion{Store: store}
	job, replayed, err := worker.Enqueue(ctx, "target", "conan-quarantine-worker", PromotionPayload{
		SourceRepositoryID: "source", Reference: reference, Revision: revision, Digest: recipeDigest,
	})
	if err != nil || replayed {
		t.Fatalf("enqueue job=%#v replayed=%t err=%v", job, replayed, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: "source", Format: repository.FormatConan,
		Coordinate: reference + "#" + revision, Digest: recipeDigest,
		State: repository.ArtifactQuarantineStateQuarantined, Reason: "block queued Conan closure", UpdatedBy: "security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}

	if err = worker.RunJobs(ctx, 1); err != nil {
		t.Fatalf("worker should persist the blocked job: %v", err)
	}
	if _, err = store.GetConanRecipeRevision(ctx, "target", reference, revision); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target Conan recipe should remain unpublished, err=%v", err)
	}
	if _, err = store.GetConanPackageRevision(ctx, "target", reference, revision, "package-id", "prev"); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target Conan package closure should remain unpublished, err=%v", err)
	}
	persisted, err := store.GetLifecycleJob(ctx, "target", job.ID)
	if err != nil || persisted.State != repository.LifecycleJobRetrying || persisted.LastError != repository.ArtifactQuarantinedReason {
		t.Fatalf("promotion job=%#v err=%v", persisted, err)
	}
}
