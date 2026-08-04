package conan

import (
	"context"
	"errors"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type failOnceConanPromotionStore struct {
	*repository.MemoryStore
	fail bool
}

func (s *failOnceConanPromotionStore) PromoteConanRecipeRevision(ctx context.Context, p repository.ConanPromotion) (repository.ConanRecipeRevision, error) {
	if s.fail {
		s.fail = false
		return repository.ConanRecipeRevision{}, errors.New("injected target metadata failure")
	}
	return s.MemoryStore.PromoteConanRecipeRevision(ctx, p)
}

func TestNativePromotionRetriesFailedConanPublication(t *testing.T) {
	ctx := context.Background()
	base := repository.NewMemoryStore()
	store := &failOnceConanPromotionStore{MemoryStore: base, fail: true}
	asset := repository.ConanAsset{RepositoryID: "source", Reference: "pkg/1.0/user/stable", RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: "conan/widget", Digest: "sha256:widget", Size: 7}
	if err := base.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: "source", ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}); err != nil {
		t.Fatal(err)
	}
	if _, err := base.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: "source", Reference: asset.Reference, Revision: asset.RecipeRevision, Digest: asset.Digest}, []repository.ConanAsset{asset}); err != nil {
		t.Fatal(err)
	}
	worker := NativePromotion{Store: store}
	job, _, err := worker.Enqueue(ctx, "target", "retry", PromotionPayload{SourceRepositoryID: "source", Reference: asset.Reference, Revision: asset.RecipeRevision, Digest: asset.Digest})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.RunJobs(ctx, 1); err != nil {
		t.Fatalf("worker must persist a retryable failure: %v", err)
	}
	jobs, err := base.ListLifecycleJobs(ctx, "target", 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].State != repository.LifecycleJobRetrying {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	if _, err = base.RunLifecycleJobNow(ctx, "target", job.ID); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if target, err := base.GetConanRecipeRevision(ctx, "target", asset.Reference, asset.RecipeRevision); err != nil || target.Digest != asset.Digest {
		t.Fatalf("target=%#v err=%v", target, err)
	}
}
