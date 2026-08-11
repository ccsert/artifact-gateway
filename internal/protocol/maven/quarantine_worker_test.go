package maven

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativePromotionWorkerBlocksArtifactQuarantinedAfterEnqueue(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	coordinate := "org.example:quarantined:1.0.0"
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	path := "org/example/quarantined/1.0.0/quarantined-1.0.0.jar"
	session := repository.MavenPublishSession{
		ID:           "quarantined-source",
		RepositoryID: "source",
		Coordinate:   coordinate,
		Publisher:    "test",
		State:        "open",
		ExpiresAt:    time.Now().Add(time.Hour),
		Objects:      []repository.MavenDeclaredObject{{Name: "quarantined-1.0.0.jar", Digest: digest, Size: 3}},
	}
	if _, err := store.CreateMavenPublishSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, "native/maven/quarantined"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMavenPublishSession(ctx, session.ID, []repository.MavenAsset{{
		RepositoryID: "source", Path: path, ObjectKey: "native/maven/quarantined", Digest: digest, Size: 3,
	}}); err != nil {
		t.Fatal(err)
	}

	worker := NativePromotion{Store: store}
	job, replayed, err := worker.Enqueue(ctx, "target", "maven-quarantine-worker", PromotionPayload{
		SourceRepositoryID: "source", Coordinate: coordinate, Digest: digest, PromotionID: "quarantined-promotion",
	})
	if err != nil || replayed {
		t.Fatalf("enqueue job=%#v replayed=%t err=%v", job, replayed, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: "source", Format: repository.FormatMaven, Coordinate: coordinate, Digest: digest,
		State: repository.ArtifactQuarantineStateQuarantined, Reason: "block queued Maven promotion", UpdatedBy: "security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}

	if err = worker.RunJobs(ctx, 1); err != nil {
		t.Fatalf("worker should persist the blocked job: %v", err)
	}
	if _, err = store.GetMavenAsset(ctx, "target", path); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target Maven artifact should remain unpublished, err=%v", err)
	}
	persisted, err := store.GetLifecycleJob(ctx, "target", job.ID)
	if err != nil || persisted.State != repository.LifecycleJobRetrying || persisted.LastError != repository.ArtifactQuarantinedReason {
		t.Fatalf("promotion job=%#v err=%v", persisted, err)
	}
}
