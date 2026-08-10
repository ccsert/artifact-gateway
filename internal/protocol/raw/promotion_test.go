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

type unavailableRawIntelligenceStore struct {
	*repository.MemoryStore
	err error
}

func (s *unavailableRawIntelligenceStore) ReplaceArtifactIntelligence(ctx context.Context, value repository.ArtifactIntelligence, expectedVersion string) (repository.ArtifactIntelligence, error) {
	if value.RepositoryID == "target" {
		return repository.ArtifactIntelligence{}, s.err
	}
	return s.MemoryStore.ReplaceArtifactIntelligence(ctx, value, expectedVersion)
}

func (s *unavailableRawIntelligenceStore) EnqueueLifecycleJob(context.Context, repository.LifecycleJob) (repository.LifecycleJob, bool, error) {
	return repository.LifecycleJob{}, false, s.err
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

func TestNativePromotionCompletesWhenIntelligenceQueueIsUnavailable(t *testing.T) {
	ctx := context.Background()
	base := repository.NewMemoryStore()
	asset := repository.RawAsset{RepositoryID: "source", Path: "releases/widget.txt", Digest: "sha256:widget", ObjectKey: "raw/widget", Size: 7}
	if _, err := base.PutRawAsset(ctx, asset); err != nil {
		t.Fatal(err)
	}
	if _, err := base.ReplaceArtifactIntelligence(ctx, repository.ArtifactIntelligence{
		RepositoryID: asset.RepositoryID,
		Format:       repository.FormatRaw,
		Coordinate:   asset.Path,
		Digest:       asset.Digest,
		Licenses:     []repository.ArtifactLicense{{SPDXID: "Apache-2.0"}},
	}, ""); err != nil {
		t.Fatal(err)
	}
	job, _, err := base.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: "promotion", RepositoryID: "target", Kind: repository.LifecycleJobPromotion, IdempotencyKey: "promotion", Payload: []byte(`{"format":"raw","sourceRepositoryId":"source","path":"releases/widget.txt","digest":"sha256:widget"}`)})
	if err != nil {
		t.Fatal(err)
	}
	store := &unavailableRawIntelligenceStore{MemoryStore: base, err: errors.New("database unavailable")}
	worker := NativePromotion{Store: store, Intelligence: store}
	if err = worker.RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = base.GetRawAsset(ctx, "target", asset.Path); err != nil {
		t.Fatalf("target asset unavailable: %v", err)
	}
	j, err := base.GetLifecycleJob(ctx, "target", job.ID)
	if err != nil || j.State != repository.LifecycleJobCompleted {
		t.Fatalf("job=%#v err=%v, want completed publication", j, err)
	}
}
