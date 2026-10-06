//go:build integration

package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
	testsupport "github.com/artifact-gateway/artifact-gateway/internal/testsupport/snapshotfixture"
	"github.com/google/uuid"
)

func TestPostgresSnapshotTakeoverSerializesWithSourceTombstone(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "takeover-delete-" + uuid.NewString(), Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	dir, digest, _, _ := testsupport.SnapshotBundle(t)
	p, _, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	objects := NewMemoryOCIObjectStore()
	if _, err = snapshotimport.Run(ctx, p, store, objects, repo.ID, "target", "operator", testsupport.SnapshotTargetBinding, true, testsupport.SnapshotCapacity(t, p, repo.ID, "target", "operator")); err != nil {
		t.Fatal(err)
	}
	plans, err := p.References(repo.ID, "target", "operator", testsupport.SnapshotTargetBinding)
	if err != nil {
		t.Fatal(err)
	}
	items, err := store.ListMavenArtifacts(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var pid int
	if err = tx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT id FROM native_maven_artifacts WHERE id=$1 FOR UPDATE`, items[0].ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := store.TakeoverMavenSnapshotImport(ctx, plans[0], "reviewed-key"); done <- err }()
	// Observe the actual database lock wait; timing alone is not the oracle.
	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("takeover passed a concurrently locked source: %v", err)
		default:
		}
		var blocked bool
		if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%native_maven_artifacts%')`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("takeover did not lock source artifacts")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE native_maven_artifacts SET state='deleted' WHERE id=$1`, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, repository.ErrMavenSnapshotTakeoverNotReady) {
			t.Fatalf("deleted source admission=%v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	checkpoint, err := store.GetMavenSnapshotImport(ctx, repo.ID, plans[0].Coordinate)
	if err != nil || checkpoint.Writable() {
		t.Fatal("deleted source acquired takeover receipt")
	}
}
