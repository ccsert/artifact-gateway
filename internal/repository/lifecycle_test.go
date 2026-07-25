package repository

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryLifecycleJobsAreIdempotentAndClaimedOnce(t *testing.T) {
	store := NewMemoryStore()
	job := LifecycleJob{ID: "job-1", RepositoryID: "repository-1", Kind: LifecycleJobReclaim, IdempotencyKey: "reclaim:object-1", Payload: []byte(`{"object":"object-1"}`)}
	created, replayed, err := store.EnqueueLifecycleJob(context.Background(), job)
	if err != nil || replayed || created.State != LifecycleJobPending {
		t.Fatalf("created=%#v replayed=%v err=%v", created, replayed, err)
	}
	replayJob := job
	replayJob.Payload = []byte("{\n  \"object\": \"object-1\"\n}")
	replay, replayed, err := store.EnqueueLifecycleJob(context.Background(), replayJob)
	if err != nil || !replayed || replay.ID != job.ID {
		t.Fatalf("replay=%#v replayed=%v err=%v", replay, replayed, err)
	}
	if _, _, err := store.EnqueueLifecycleJob(context.Background(), LifecycleJob{ID: "other", RepositoryID: job.RepositoryID, Kind: job.Kind, IdempotencyKey: job.IdempotencyKey, Payload: []byte(`{"object":"other"}`)}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting replay error=%v", err)
	}
	claimed, err := store.ClaimLifecycleJobs(context.Background(), 10)
	if err != nil || len(claimed) != 1 || claimed[0].State != LifecycleJobRunning {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	claimed, err = store.ClaimLifecycleJobs(context.Background(), 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("second claim=%#v err=%v", claimed, err)
	}
	if err := store.CompleteLifecycleJob(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLifecycleJob(context.Background(), job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat completion error=%v", err)
	}
}
