//go:build integration

package repository

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresQuarantineReadPolicyPersistsCASAndCascades(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	first, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPostgresStore(databaseURL)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := first.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "quarantine-read-policy-" + uuid.NewString(), Format: FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = first.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = first.Close()
		_ = second.Close()
	})
	initial, err := first.GetRepositoryQuarantineReadPolicy(ctx, repo.ID)
	if err != nil || initial.Version != "1" || initial.Enabled {
		t.Fatalf("initial=%#v err=%v", initial, err)
	}
	initial.Enabled = true
	updated, err := first.ReplaceRepositoryQuarantineReadPolicy(ctx, repo.ID, initial, initial.Version)
	if err != nil || updated.Version != "2" || !updated.Enabled {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err = second.ReplaceRepositoryQuarantineReadPolicy(ctx, repo.ID, initial, "1"); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("cross-connection stale update=%v", err)
	}
	loaded, err := second.GetRepositoryQuarantineReadPolicy(ctx, repo.ID)
	if err != nil || loaded != updated {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	if _, err = first.db.ExecContext(ctx, `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = second.db.QueryRowContext(ctx, `SELECT count(*) FROM repository_quarantine_read_policies WHERE repository_id=$1`, repo.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade count=%d err=%v", count, err)
	}
}
