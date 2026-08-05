package app

import (
	"context"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestRepositoryDeletionWorkerFinalizesDeletingRepositories(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	created, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID:     "repository-deletion-worker",
		Name:   "repository-deletion-worker",
		Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DisableHostedRepository(ctx, created.ID); err != nil {
		t.Fatal(err)
	}

	worker := RepositoryDeletionWorker{Store: store}
	finalized, err := worker.Run(ctx)
	if err != nil || finalized != 1 {
		t.Fatalf("finalized=%d err=%v", finalized, err)
	}
	current, err := store.GetHostedRepository(ctx, created.ID)
	if err != nil || current.State != repository.RepositoryDeleted {
		t.Fatalf("repository=%#v err=%v", current, err)
	}

	finalized, err = worker.Run(ctx)
	if err != nil || finalized != 0 {
		t.Fatalf("second run finalized=%d err=%v", finalized, err)
	}
}
