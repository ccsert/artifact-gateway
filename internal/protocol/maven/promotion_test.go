package maven

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type failOncePromotionStore struct {
	*repository.MemoryStore
	fail bool
}

func (s *failOncePromotionStore) PromoteMavenArtifact(ctx context.Context, promotion repository.MavenPromotion) (repository.MavenArtifact, error) {
	if s.fail {
		s.fail = false
		return repository.MavenArtifact{}, errors.New("temporary PostgreSQL failure")
	}
	return s.MemoryStore.PromoteMavenArtifact(ctx, promotion)
}

func TestNativePromotionRetriesFailedJobAndKeepsSourceVisible(t *testing.T) {
	ctx := context.Background()
	base := repository.NewMemoryStore()
	source, err := base.CreateHostedRepository(ctx, repository.HostedRepository{ID: "source", Name: "source", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	target, err := base.CreateHostedRepository(ctx, repository.HostedRepository{ID: "target", Name: "target", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	session := repository.MavenPublishSession{ID: "source-session", RepositoryID: source.ID, Coordinate: "org.example:widget:1.0.0", Publisher: "test", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "widget-1.0.0.jar", Digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 3}}}
	if _, err = base.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err = base.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, "native/maven/widget"); err != nil {
		t.Fatal(err)
	}
	if _, err = base.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{RepositoryID: source.ID, Path: "org/example/widget/1.0.0/widget-1.0.0.jar", ObjectKey: "native/maven/widget", Digest: session.Objects[0].Digest, Size: 3}}); err != nil {
		t.Fatal(err)
	}
	if _, err = base.ReplaceArtifactIntelligence(ctx, repository.ArtifactIntelligence{
		RepositoryID: source.ID,
		Format:       repository.FormatMaven,
		Coordinate:   session.Coordinate,
		Digest:       session.Objects[0].Digest,
		SBOMs:        []repository.ArtifactSBOM{{MediaType: "application/spdx+json", Digest: session.Objects[0].Digest}},
	}, ""); err != nil {
		t.Fatal(err)
	}

	store := &failOncePromotionStore{MemoryStore: base, fail: true}
	worker := NativePromotion{Store: store, Intelligence: store}
	job, replayed, err := worker.Enqueue(ctx, target.ID, "promotion-1", PromotionPayload{SourceRepositoryID: source.ID, Coordinate: session.Coordinate, Digest: session.Objects[0].Digest, PromotionID: "promoted"})
	if err != nil || replayed {
		t.Fatalf("enqueue job=%#v replayed=%t err=%v", job, replayed, err)
	}
	if _, replayed, err = worker.Enqueue(ctx, target.ID, "promotion-1", PromotionPayload{SourceRepositoryID: source.ID, Coordinate: session.Coordinate, Digest: session.Objects[0].Digest, PromotionID: "promoted"}); err != nil || !replayed {
		t.Fatalf("idempotent enqueue replayed=%t err=%v", replayed, err)
	}
	if err = worker.RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err := base.ListLifecycleJobs(ctx, target.ID, 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobRetrying {
		t.Fatalf("after transient failure jobs=%#v err=%v", jobs, err)
	}
	if _, err = base.RunLifecycleJobNow(ctx, target.ID, job.ID); err != nil {
		t.Fatal(err)
	}
	if err = worker.RunJobs(ctx, 1); err != nil {
		t.Fatal(err)
	}
	jobs, err = base.ListLifecycleJobs(ctx, target.ID, 10)
	if err != nil || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("after retry jobs=%#v err=%v", jobs, err)
	}
	if _, err = base.GetMavenArtifactByCoordinate(ctx, source.ID, session.Coordinate); err != nil {
		t.Fatalf("source artifact lost: %v", err)
	}
	if _, err = base.GetMavenAsset(ctx, target.ID, "org/example/widget/1.0.0/widget-1.0.0.jar"); err != nil {
		t.Fatalf("promoted asset unavailable: %v", err)
	}
	if copied, err := base.GetArtifactIntelligence(ctx, target.ID, repository.FormatMaven, session.Coordinate, session.Objects[0].Digest); err != nil || len(copied.SBOMs) != 1 {
		t.Fatalf("promoted intelligence=%#v err=%v", copied, err)
	}
}
