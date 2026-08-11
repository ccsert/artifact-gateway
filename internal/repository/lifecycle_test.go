package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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
	leaseToken := claimed[0].LeaseToken
	claimed, err = store.ClaimLifecycleJobs(context.Background(), 10)
	if err != nil || len(claimed) != 0 {
		t.Fatalf("second claim=%#v err=%v", claimed, err)
	}
	if err := store.CompleteLifecycleJob(context.Background(), job.ID, leaseToken); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteLifecycleJob(context.Background(), job.ID, leaseToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("repeat completion error=%v", err)
	}
}

func TestMemoryLifecycleJobsFindLatestArtifactScanByImmutableIdentity(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	payload := ArtifactScanPayload{Format: FormatRaw, Coordinate: "release/widget.bin", Digest: "sha256:" + strings.Repeat("a", 64)}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: "first", RepositoryID: "repo", Kind: LifecycleJobScan, IdempotencyKey: "first", Payload: body})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	second, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: "second", RepositoryID: "repo", Kind: LifecycleJobScan, IdempotencyKey: "second", Payload: body})
	if err != nil {
		t.Fatal(err)
	}
	latest, err := store.GetLatestArtifactScanJob(ctx, "repo", FormatRaw, payload.Coordinate, payload.Digest)
	if err != nil || latest.ID != second.ID || latest.ID == first.ID {
		t.Fatalf("latest=%#v err=%v", latest, err)
	}
	if _, err = store.GetLatestArtifactScanJob(ctx, "repo", FormatRaw, "missing.bin", payload.Digest); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing error=%v", err)
	}
}

func TestMemoryArtifactScanEnqueueDeduplicatesActiveIdentity(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	payload := ArtifactScanPayload{Format: FormatRaw, Coordinate: "release/widget.bin", Digest: "sha256:" + strings.Repeat("b", 64)}
	first, replayed, err := EnqueueArtifactScanJob(ctx, store, "repo", "manual-1", payload)
	if err != nil || replayed {
		t.Fatalf("first=%#v replayed=%v err=%v", first, replayed, err)
	}
	second, replayed, err := EnqueueArtifactScanJob(ctx, store, "repo", "manual-2", payload)
	if err != nil || !replayed || second.ID != first.ID {
		t.Fatalf("second=%#v replayed=%v err=%v", second, replayed, err)
	}
}

func TestMemoryLifecycleJobsCanBeClaimedByKind(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if _, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: "reclaim", RepositoryID: "repository-1", Kind: LifecycleJobReclaim, IdempotencyKey: "reclaim-1", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: "retention", RepositoryID: "repository-1", Kind: LifecycleJobRetention, IdempotencyKey: "retention-1", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimLifecycleJobsByKind(ctx, LifecycleJobReclaim, 10)
	if err != nil || len(claimed) != 1 || claimed[0].ID != "reclaim" {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = store.CompleteLifecycleJob(ctx, claimed[0].ID, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.ClaimLifecycleJobsByKind(ctx, LifecycleJobRetention, 10)
	if err != nil || len(remaining) != 1 || remaining[0].ID != "retention" {
		t.Fatalf("remaining=%#v err=%v", remaining, err)
	}
}

func TestMemoryLifecycleJobsCanBeClaimedByFormat(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	for _, format := range []Format{FormatOCI, FormatMaven} {
		if _, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: string(format), RepositoryID: "repository-1", Kind: LifecycleJobReclaim, IdempotencyKey: string(format), Payload: []byte(`{"format":"` + string(format) + `"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := store.ClaimLifecycleJobsByKindAndFormat(ctx, LifecycleJobReclaim, FormatOCI, 10)
	if err != nil || len(claimed) != 1 || claimed[0].ID != "oci" {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = store.CompleteLifecycleJob(ctx, claimed[0].ID, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.ClaimLifecycleJobsByKindAndFormat(ctx, LifecycleJobReclaim, FormatMaven, 10)
	if err != nil || len(remaining) != 1 || remaining[0].ID != "maven" {
		t.Fatalf("remaining=%#v err=%v", remaining, err)
	}
}

func TestMemoryLifecycleClaimLimitsEachRepositoryToOneRunningJob(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	for _, job := range []LifecycleJob{
		{ID: "one", RepositoryID: "repo", Kind: LifecycleJobReclaim, IdempotencyKey: "one", Payload: []byte(`{"format":"raw"}`)},
		{ID: "two", RepositoryID: "repo", Kind: LifecycleJobPromotion, IdempotencyKey: "two", Payload: []byte(`{"format":"raw"}`)},
		{ID: "three", RepositoryID: "other", Kind: LifecycleJobReclaim, IdempotencyKey: "three", Payload: []byte(`{"format":"raw"}`)},
	} {
		if _, _, err := store.EnqueueLifecycleJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := store.ClaimLifecycleJobs(ctx, 10)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	for _, job := range claimed {
		if job.RepositoryID == "repo" {
			if err = store.CompleteLifecycleJob(ctx, job.ID, job.LeaseToken); err != nil {
				t.Fatal(err)
			}
		}
	}
	next, err := store.ClaimLifecycleJobs(ctx, 10)
	if err != nil || len(next) != 1 || next[0].RepositoryID != "repo" {
		t.Fatalf("next=%#v err=%v", next, err)
	}
}

func TestMemoryLifecycleJobRetriesWithBackoffBeforeFinalFailure(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	created, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: "retry-job", RepositoryID: "repo", Kind: LifecycleJobRetention, IdempotencyKey: "retry-job", Payload: []byte(`{"format":"raw"}`), MaxAttempts: 3})
	if err != nil || created.MaxAttempts != 3 || created.Attempts != 0 {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		claimed, claimErr := store.ClaimLifecycleJobs(ctx, 1)
		if claimErr != nil || len(claimed) != 1 || claimed[0].Attempts != attempt || claimed[0].LeaseExpiresAt.IsZero() {
			t.Fatalf("attempt %d claimed=%#v err=%v", attempt, claimed, claimErr)
		}
		if err = store.FailLifecycleJob(ctx, "retry-job", claimed[0].LeaseToken, "temporary outage"); err != nil {
			t.Fatal(err)
		}
		jobs, listErr := store.ListLifecycleJobs(ctx, "repo", 10)
		if listErr != nil || len(jobs) != 1 {
			t.Fatalf("attempt %d jobs=%#v err=%v", attempt, jobs, listErr)
		}
		if attempt < 3 {
			if jobs[0].State != LifecycleJobRetrying || !jobs[0].NextAttemptAt.After(time.Now().UTC()) || !jobs[0].CompletedAt.IsZero() {
				t.Fatalf("attempt %d retry state=%#v", attempt, jobs[0])
			}
			if claimed, claimErr = store.ClaimLifecycleJobs(ctx, 1); claimErr != nil || len(claimed) != 0 {
				t.Fatalf("attempt %d ignored backoff claimed=%#v err=%v", attempt, claimed, claimErr)
			}
			if _, err = store.RunLifecycleJobNow(ctx, "repo", "retry-job"); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if jobs[0].State != LifecycleJobFailed || jobs[0].CompletedAt.IsZero() || jobs[0].LastError != "temporary outage" {
			t.Fatalf("final state=%#v", jobs[0])
		}
	}
}

func TestMemoryLifecycleJobRecoversExpiredLeaseAndSupportsControls(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if _, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: "controlled", RepositoryID: "repo", Kind: LifecycleJobReclaim, IdempotencyKey: "controlled", Payload: []byte(`{"format":"raw"}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	recovered, err := store.RecoverExpiredLifecycleJobs(ctx, claimed[0].LeaseExpiresAt.Add(time.Second))
	if err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	recoveredJob, err := store.GetLifecycleJob(ctx, "repo", "controlled")
	if err != nil || recoveredJob.State != LifecycleJobRetrying || !recoveredJob.NextAttemptAt.After(claimed[0].LeaseExpiresAt) {
		t.Fatalf("recovered job=%#v err=%v", recoveredJob, err)
	}
	job, err := store.RunLifecycleJobNow(ctx, "repo", "controlled")
	if err != nil || job.State != LifecycleJobPending {
		t.Fatalf("run now=%#v err=%v", job, err)
	}
	job, err = store.CancelLifecycleJob(ctx, "repo", "controlled")
	if err != nil || job.State != LifecycleJobCancelled || job.CompletedAt.IsZero() {
		t.Fatalf("cancelled=%#v err=%v", job, err)
	}
	if _, err = store.RunLifecycleJobNow(ctx, "repo", "controlled"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("run cancelled error=%v", err)
	}

	if _, _, err = store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: "failed", RepositoryID: "repo", Kind: LifecycleJobReclaim, IdempotencyKey: "failed", Payload: []byte(`{"format":"raw"}`), MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	if claimed, err = store.ClaimLifecycleJobs(ctx, 1); err != nil || len(claimed) != 1 || claimed[0].ID != "failed" {
		t.Fatalf("failed claim=%#v err=%v", claimed, err)
	}
	if err = store.FailLifecycleJob(ctx, "failed", claimed[0].LeaseToken, "permanent"); err != nil {
		t.Fatal(err)
	}
	job, err = store.RetryLifecycleJob(ctx, "repo", "failed")
	if err != nil || job.State != LifecycleJobPending || job.Attempts != 0 || job.LastError != "" {
		t.Fatalf("retried=%#v err=%v", job, err)
	}
}

func TestMemoryLifecycleJobsRequeueFailedJobsByKind(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	jobs := []LifecycleJob{
		{ID: "intelligence-one", RepositoryID: "repo", Kind: LifecycleJobIntelligence, IdempotencyKey: "intelligence-one", Payload: []byte(`{"format":"oci"}`), MaxAttempts: 1},
		{ID: "intelligence-two", RepositoryID: "repo", Kind: LifecycleJobIntelligence, IdempotencyKey: "intelligence-two", Payload: []byte(`{"format":"oci"}`), MaxAttempts: 1},
		{ID: "retention", RepositoryID: "repo", Kind: LifecycleJobRetention, IdempotencyKey: "retention", Payload: []byte(`{"format":"oci"}`), MaxAttempts: 1},
	}
	for _, job := range jobs {
		if _, _, err := store.EnqueueLifecycleJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []LifecycleJobKind{LifecycleJobIntelligence, LifecycleJobIntelligence, LifecycleJobRetention} {
		claimed, err := store.ClaimLifecycleJobsByKind(ctx, kind, 1)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("kind=%s claimed=%#v err=%v", kind, claimed, err)
		}
		if err = store.FailLifecycleJob(ctx, claimed[0].ID, claimed[0].LeaseToken, "dependency unavailable"); err != nil {
			t.Fatal(err)
		}
	}
	requeued, err := store.RequeueFailedLifecycleJobs(ctx, "repo", LifecycleJobIntelligence, 1)
	if err != nil || len(requeued) != 1 || requeued[0].ID != "intelligence-one" || requeued[0].State != LifecycleJobPending || requeued[0].Attempts != 0 || requeued[0].LastError != "" {
		t.Fatalf("requeued=%#v err=%v", requeued, err)
	}
	retention, err := store.GetLifecycleJob(ctx, "repo", "retention")
	if err != nil || retention.State != LifecycleJobFailed {
		t.Fatalf("retention=%#v err=%v", retention, err)
	}
	remaining, err := store.RequeueFailedLifecycleJobs(ctx, "repo", LifecycleJobIntelligence, 100)
	if err != nil || len(remaining) != 1 || remaining[0].ID != "intelligence-two" {
		t.Fatalf("remaining=%#v err=%v", remaining, err)
	}
}

func TestMemoryLifecycleJobLeaseTokenFencesExpiredWorkerAndProgressRenewsLease(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if _, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{ID: "fenced", RepositoryID: "repo", Kind: LifecycleJobRetention, IdempotencyKey: "fenced", Payload: []byte(`{"format":"raw"}`)}); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(first) != 1 || first[0].LeaseToken == "" {
		t.Fatalf("first claim=%#v err=%v", first, err)
	}
	firstLeaseExpiry := first[0].LeaseExpiresAt
	if err = store.UpdateLifecycleJobProgress(ctx, first[0].ID, first[0].LeaseToken, 1, 2, "halfway"); err != nil {
		t.Fatal(err)
	}
	progressed, err := store.GetLifecycleJob(ctx, "repo", "fenced")
	if err != nil || !progressed.LeaseExpiresAt.After(firstLeaseExpiry) {
		t.Fatalf("progressed=%#v err=%v", progressed, err)
	}
	if _, err = store.RecoverExpiredLifecycleJobs(ctx, progressed.LeaseExpiresAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RunLifecycleJobNow(ctx, "repo", "fenced"); err != nil {
		t.Fatal(err)
	}
	second, err := store.ClaimLifecycleJobs(ctx, 1)
	if err != nil || len(second) != 1 || second[0].LeaseToken == first[0].LeaseToken {
		t.Fatalf("second claim=%#v err=%v", second, err)
	}
	if err = store.UpdateLifecycleJobProgress(ctx, first[0].ID, first[0].LeaseToken, 2, 2, "stale"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale progress error=%v", err)
	}
	if err = store.CompleteLifecycleJob(ctx, first[0].ID, first[0].LeaseToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale completion error=%v", err)
	}
	if err = store.CompleteLifecycleJob(ctx, second[0].ID, second[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
}
