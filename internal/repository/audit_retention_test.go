package repository

import (
	"context"
	"testing"
	"time"
)

func TestAuditCleanupClaimReclaimsExpiredRunningJobs(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, _, err := store.EnqueueAuditCleanupJob(ctx, AuditCleanupJob{ID: "fresh", IdempotencyKey: "fresh", PolicyVersion: "1", CutoffAt: time.Now(), BatchSize: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnqueueAuditCleanupJob(ctx, AuditCleanupJob{ID: "expired", IdempotencyKey: "expired", PolicyVersion: "1", CutoffAt: time.Now(), BatchSize: 1}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimAuditCleanupJobs(ctx, 2)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("initial claim=%#v err=%v", claimed, err)
	}
	if claimed, err = store.ClaimAuditCleanupJobs(ctx, 2); err != nil || len(claimed) != 0 {
		t.Fatalf("fresh running claim=%#v err=%v", claimed, err)
	}

	store.mu.Lock()
	expired := store.auditCleanupJobs["expired"]
	expired.StartedAt = time.Now().UTC().Add(-auditCleanupJobLease - time.Second)
	store.auditCleanupJobs["expired"] = expired
	store.mu.Unlock()

	claimed, err = store.ClaimAuditCleanupJobs(ctx, 2)
	if err != nil || len(claimed) != 1 || claimed[0].ID != "expired" || claimed[0].State != LifecycleJobRunning {
		t.Fatalf("expired claim=%#v err=%v", claimed, err)
	}
}
