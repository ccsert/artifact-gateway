//go:build integration

package repository

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresSecurityPolicyPersistsAutoScanOnPublish(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "security-policy-" + uuid.NewString(), Format: FormatRaw,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM repository_security_policies WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = store.Close()
	})

	policy, err := store.GetRepositorySecurityPolicy(ctx, repo.ID)
	if err != nil || policy.Version != "1" || policy.AutoScanOnPublish {
		t.Fatalf("initial policy=%#v err=%v", policy, err)
	}
	policy.AutoScanOnPublish = true
	updated, err := store.ReplaceRepositorySecurityPolicy(ctx, repo.ID, policy, policy.Version)
	if err != nil || updated.Version != "2" || !updated.AutoScanOnPublish {
		t.Fatalf("updated policy=%#v err=%v", updated, err)
	}
	loaded, err := store.GetRepositorySecurityPolicy(ctx, repo.ID)
	if err != nil || loaded.Version != "2" || !loaded.AutoScanOnPublish {
		t.Fatalf("loaded policy=%#v err=%v", loaded, err)
	}

	loaded.AutoScanOnPublish = false
	disabled, err := store.ReplaceRepositorySecurityPolicy(ctx, repo.ID, loaded, loaded.Version)
	if err != nil || disabled.Version != "3" || disabled.AutoScanOnPublish {
		t.Fatalf("disabled policy=%#v err=%v", disabled, err)
	}
	reloaded, err := store.GetRepositorySecurityPolicy(ctx, repo.ID)
	if err != nil || reloaded.Version != "3" || reloaded.AutoScanOnPublish {
		t.Fatalf("reloaded policy=%#v err=%v", reloaded, err)
	}
}
