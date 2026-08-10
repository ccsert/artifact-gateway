package app

import (
	"context"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestArtifactIntelligenceCopyWorkerCompletesFollowUpJob(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	value := repository.ArtifactIntelligence{
		RepositoryID: "source",
		Format:       repository.FormatRaw,
		Coordinate:   "releases/widget.zip",
		Digest:       "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Licenses:     []repository.ArtifactLicense{{SPDXID: "Apache-2.0"}},
	}
	if _, err := store.ReplaceArtifactIntelligence(ctx, value, ""); err != nil {
		t.Fatal(err)
	}
	job, replayed, err := repository.EnqueueArtifactIntelligenceCopyJob(ctx, store, "target", "source", value.Format, value.Coordinate, value.Digest)
	if err != nil || replayed {
		t.Fatalf("enqueue job=%#v replayed=%t err=%v", job, replayed, err)
	}
	worker := ArtifactIntelligenceCopyWorker{Store: store}
	if err = worker.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetArtifactIntelligence(ctx, "target", value.Format, value.Coordinate, value.Digest)
	if err != nil || stored.Licenses[0].SPDXID != "Apache-2.0" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	jobs, err := store.ListLifecycleJobs(ctx, "target", 10)
	if err != nil || len(jobs) != 1 || jobs[0].State != repository.LifecycleJobCompleted {
		t.Fatalf("jobs=%#v err=%v", jobs, err)
	}
}

func TestArtifactIntelligenceCopyWorkerRespectsFormatFilter(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	job, _, err := repository.EnqueueArtifactIntelligenceCopyJob(ctx, store, "target", "source", repository.FormatRaw, "releases/widget.zip", "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	worker := ArtifactIntelligenceCopyWorker{Store: store, WorkerFormats: []string{"oci"}}
	if err = worker.RunJobs(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetLifecycleJob(ctx, "target", job.ID)
	if err != nil || stored.State != repository.LifecycleJobPending {
		t.Fatalf("filtered job=%#v err=%v", stored, err)
	}
}
