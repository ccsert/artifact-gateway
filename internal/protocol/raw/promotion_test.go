package raw

import (
	"context"
	"errors"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type failOnceRawPromotionStore struct {
	*repository.MemoryStore
	fail bool
}

func (s *failOnceRawPromotionStore) PutRawAsset(ctx context.Context, asset repository.RawAsset) (repository.RawAsset, error) {
	if s.fail {
		s.fail = false
		return repository.RawAsset{}, errors.New("injected target metadata failure")
	}
	return s.MemoryStore.PutRawAsset(ctx, asset)
}

func TestNativePromotionRetriesFailedRawPublication(t *testing.T) {
	ctx := context.Background()
	base := repository.NewMemoryStore()
	store := &failOnceRawPromotionStore{MemoryStore: base, fail: true}
	if _, err := base.PutRawAsset(ctx, repository.RawAsset{RepositoryID: "source", Path: "releases/widget.txt", Digest: "sha256:widget", ObjectKey: "raw/widget", Size: 7}); err != nil {
		t.Fatal(err)
	}
	worker := NativePromotion{Store: store}
	job, _, err := worker.Enqueue(ctx, "target", "retry", PromotionPayload{SourceRepositoryID: "source", Path: "releases/widget.txt", Digest: "sha256:widget"})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.RunJobs(ctx, 1); err == nil {
		t.Fatal("first run must fail")
	}
	jobs, err := base.ListLifecycleJobs(ctx, "target", 10)
	if err != nil || len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].State != repository.LifecycleJobRetrying {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
	if _, err = base.GetRawAsset(ctx, "source", "releases/widget.txt"); err != nil {
		t.Fatalf("source disappeared after failed promotion: %v", err)
	}
	if _, err = base.RunLifecycleJobNow(ctx, "target", job.ID); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if target, err := base.GetRawAsset(ctx, "target", "releases/widget.txt"); err != nil || target.Digest != "sha256:widget" {
		t.Fatalf("target=%#v err=%v", target, err)
	}
}
