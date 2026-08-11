//go:build integration

package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	rawprotocol "github.com/artifact-gateway/artifact-gateway/internal/protocol/raw"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresRawPromotionWorkerDoesNotPublishQuarantinedSource(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	suffix := uuid.NewString()
	source, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "quarantine-worker-source-" + suffix, Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "quarantine-worker-target-" + suffix, Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	coordinate := "releases/postgres-worker.bin"
	digest := "sha256:" + strings.Repeat("b", 64)
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{
		RepositoryID: source.ID,
		Path:         coordinate,
		Digest:       digest,
		ObjectKey:    "native/raw/quarantine-postgres-worker",
		Size:         17,
	}); err != nil {
		t.Fatal(err)
	}

	promotion := rawprotocol.NativePromotion{Store: store}
	job, replayed, err := promotion.Enqueue(ctx, target.ID, "quarantine-postgres-worker-"+suffix, rawprotocol.PromotionPayload{
		SourceRepositoryID: source.ID,
		Path:               coordinate,
		Digest:             digest,
	})
	if err != nil || replayed {
		t.Fatalf("enqueue job=%#v replayed=%t err=%v", job, replayed, err)
	}
	if _, err = store.ReplaceArtifactQuarantine(ctx, repository.ArtifactQuarantine{
		RepositoryID: source.ID,
		Format:       source.Format,
		Coordinate:   coordinate,
		Digest:       digest,
		State:        repository.ArtifactQuarantineStateQuarantined,
		Reason:       "block queued PostgreSQL promotion",
		UpdatedBy:    "user:security-admin",
	}, "0"); err != nil {
		t.Fatal(err)
	}

	if err = promotion.RunJobs(ctx, 1); err == nil || !strings.Contains(err.Error(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("promotion worker err=%v", err)
	}
	if _, err = store.GetRawAsset(ctx, target.ID, coordinate); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("target artifact should remain unpublished, err=%v", err)
	}
	persisted, err := store.GetLifecycleJob(ctx, target.ID, job.ID)
	if err != nil || persisted.State != repository.LifecycleJobRetrying || persisted.LastError != repository.ArtifactQuarantinedReason {
		t.Fatalf("promotion job=%#v err=%v", persisted, err)
	}
}
