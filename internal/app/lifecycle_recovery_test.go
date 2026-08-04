package app

import (
	"context"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestLifecycleJobRecoveryRequeuesExpiredWorkerLease(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	if _, _, err := store.EnqueueLifecycleJob(ctx, repository.LifecycleJob{ID: "expired", RepositoryID: "repo", Kind: repository.LifecycleJobReclaim, IdempotencyKey: "expired", Payload: []byte(`{"format":"raw"}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	recovery := LifecycleJobRecovery{Store: store, Now: func() time.Time { return claimed[0].LeaseExpiresAt.Add(time.Second) }}
	if recovered, err := recovery.Run(ctx); err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	job, err := store.GetLifecycleJob(ctx, "repo", "expired")
	if err != nil || job.State != repository.LifecycleJobRetrying {
		t.Fatalf("job=%#v err=%v", job, err)
	}
}
