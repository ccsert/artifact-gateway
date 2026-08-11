package npm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativePromotionWorkerBlocksArtifactQuarantinedAfterEnqueue(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	packageName, version := "quarantined-widget", "1.0.0"
	coordinate := packageName + "@" + version
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := store.PublishNPMVersion(ctx, repository.NPMVersion{
		RepositoryID: "source", PackageName: packageName, Version: version,
		Digest: digest, ObjectKey: "native/npm/source/quarantined-widget", Size: 3,
	}, map[string]string{"latest": version}); err != nil {
		t.Fatal(err)
	}

	worker := NativePromotion{Store: store}
	job, replayed, err := worker.Enqueue(ctx, "target", "npm-quarantine-worker", PromotionPayload{
		SourceRepositoryID: "source", PackageName: packageName, Version: version, Digest: digest,
	})
	if err != nil || replayed {
		t.Fatalf("enqueue job=%#v replayed=%t err=%v", job, replayed, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: "source", Format: repository.FormatNPM, Coordinate: coordinate, Digest: digest,
		State: repository.ArtifactQuarantineStateQuarantined, Reason: "block queued npm promotion", UpdatedBy: "security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}

	if err = worker.RunJobs(ctx, 1); err == nil || !strings.Contains(err.Error(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("promotion worker err=%v", err)
	}
	if _, err = store.GetNPMVersion(ctx, "target", packageName, version); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target npm version should remain unpublished, err=%v", err)
	}
	persisted, err := store.GetLifecycleJob(ctx, "target", job.ID)
	if err != nil || persisted.State != repository.LifecycleJobRetrying || persisted.LastError != repository.ArtifactQuarantinedReason {
		t.Fatalf("promotion job=%#v err=%v", persisted, err)
	}
}
