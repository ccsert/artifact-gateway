//go:build integration

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresLifecycleProgressAndRenewalStrictlyExtendLease(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	ctx := context.Background()
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "lifecycle-lease-" + uuid.NewString(), Format: FormatAPT, Type: RepositoryTypeHosted,
	})
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.EnqueueLifecycleJob(ctx, LifecycleJob{
		ID: uuid.NewString(), RepositoryID: repo.ID, Kind: LifecycleJobRetention,
		IdempotencyKey: "strict-lease", Payload: []byte(`{"format":"apt"}`), ProgressTotal: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	// APT retention is not an admitted production worker yet, which keeps this
	// storage-level lease test isolated even while the local Gateway is running.
	claimed, err := store.ClaimLifecycleJobsByKindAndFormat(ctx, LifecycleJobRetention, FormatAPT, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != job.ID {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	if err = store.UpdateLifecycleJobProgress(ctx, job.ID, claimed[0].LeaseToken, 1, 2, "halfway"); err != nil {
		t.Fatal(err)
	}
	progressed, err := store.GetLifecycleJob(ctx, repo.ID, job.ID)
	if err != nil || !progressed.LeaseExpiresAt.After(claimed[0].LeaseExpiresAt) {
		t.Fatalf("progressed=%#v claimed=%#v err=%v", progressed, claimed[0], err)
	}
	if err = store.RenewLifecycleJobLease(ctx, job.ID, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	renewed, err := store.GetLifecycleJob(ctx, repo.ID, job.ID)
	if err != nil || !renewed.LeaseExpiresAt.After(progressed.LeaseExpiresAt) {
		t.Fatalf("renewed=%#v progressed=%#v err=%v", renewed, progressed, err)
	}
}
