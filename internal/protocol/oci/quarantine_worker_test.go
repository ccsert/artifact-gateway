package oci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestNativePromotionWorkerBlocksArtifactQuarantinedAfterEnqueue(t *testing.T) {
	ctx := context.Background()
	store, objects := repository.NewMemoryStore(), objectstore.NewMemoryStore()
	body := []byte(`{"schemaVersion":2}`)
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	name, sourceKey := "team/quarantined", "native/oci/source/quarantined"
	if err := objects.Put(ctx, sourceKey, body); err != nil {
		t.Fatal(err)
	}
	if err := store.StageOCIObjectIntent(ctx, repository.OCIObjectIntent{
		RepositoryID: "source", ObjectKey: sourceKey, Digest: digest, Size: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{
		RepositoryID: "source", Name: name, Digest: digest, ObjectKey: sourceKey, Size: int64(len(body)),
	}, digest); err != nil {
		t.Fatal(err)
	}

	worker := NativePromotion{Store: store, Objects: objects}
	job, replayed, err := worker.Enqueue(ctx, "target", "oci-quarantine-worker", PromotionPayload{
		SourceRepositoryID: "source", Name: name, Digest: digest,
	})
	if err != nil || replayed {
		t.Fatalf("enqueue job=%#v replayed=%t err=%v", job, replayed, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: "source", Format: repository.FormatOCI, Coordinate: name, Digest: digest,
		State: repository.ArtifactQuarantineStateQuarantined, Reason: "block queued OCI promotion", UpdatedBy: "security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}

	if err = worker.RunJobs(ctx, 1); err == nil || !strings.Contains(err.Error(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("promotion worker err=%v", err)
	}
	if _, err = store.GetOCIManifest(ctx, "target", name, digest); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target OCI manifest should remain unpublished, err=%v", err)
	}
	persisted, err := store.GetLifecycleJob(ctx, "target", job.ID)
	if err != nil || persisted.State != repository.LifecycleJobRetrying || persisted.LastError != repository.ArtifactQuarantinedReason {
		t.Fatalf("promotion job=%#v err=%v", persisted, err)
	}
}
