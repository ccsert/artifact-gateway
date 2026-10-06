//go:build integration

package app

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
	testsupport "github.com/artifact-gateway/artifact-gateway/internal/testsupport/snapshotfixture"
	"github.com/google/uuid"
)

func TestPostgresSnapshotImportAndPublicationShareQuotaLockOrder(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	open := func(label string) *repository.PostgresStore {
		t.Helper()
		u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("application_name", label)
		u.RawQuery = q.Encode()
		s, err := repository.NewPostgresStore(u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	}
	label := uuid.NewString()
	importer, publisher := open("import-"+label), open("publish-"+label)
	repo, err := importer.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "quota-" + label, Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	// A zero quota is unlimited, but its existing row still participates in locking.
	if _, err = importer.ReplaceRepositoryCapacityQuota(ctx, repo.ID, 0); err != nil {
		t.Fatal(err)
	}
	dir, digest, _, _ := testsupport.SnapshotBundle(t)
	p, _, err := snapshotimport.Prepare(ctx, dir, digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	plans, err := p.References(repo.ID, "target", "operator", testsupport.SnapshotTargetBinding)
	if err != nil {
		t.Fatal(err)
	}
	plan := plans[0]
	checkpoint, err := importer.BeginMavenSnapshotImport(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	var shared repository.MavenAsset
	for _, a := range plan.Assets {
		name := strings.TrimPrefix(a.Path, "org/example/widget/1.0-SNAPSHOT/")
		if err = importer.MarkMavenPublishObject(ctx, checkpoint.SessionID, name, a.ObjectKey); err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, ".jar") && !strings.Contains(name, "-sources") {
			shared = a
		}
	}
	if shared.ObjectKey == "" {
		t.Fatal("missing synthetic shared JAR")
	}
	session, err := publisher.CreateMavenPublishSession(ctx, repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Coordinate: "org.example:other:1.0-SNAPSHOT", Publisher: "producer", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: "other-1.0-SNAPSHOT.jar", Digest: shared.Digest, Size: shared.Size}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = publisher.MarkMavenPublishObject(ctx, session.ID, session.Objects[0].Name, shared.ObjectKey); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	barrier, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = barrier.Rollback() }()
	var barrierPID int
	if err = barrier.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&barrierPID); err != nil {
		t.Fatal(err)
	}
	if _, err = barrier.ExecContext(ctx, `SELECT object_key FROM native_maven_object_intents WHERE object_key=$1 FOR UPDATE`, shared.ObjectKey); err != nil {
		t.Fatal(err)
	}
	importDone, publishDone := make(chan error, 1), make(chan error, 1)
	go func() { _, e := importer.CommitMavenSnapshotImport(ctx, plan); importDone <- e }()
	waitBlocked := func(application string, blocker int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var blocked bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND cardinality(pg_blocking_pids(pid))>0 AND ($2=0 OR $2=ANY(pg_blocking_pids(pid))))`, application, blocker).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not reach database lock barrier", application)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	// Import is the first waiter for the shared CAS intent. Previously the
	// publisher acquired quota while waiting behind it, creating a real cycle.
	waitBlocked("import-"+label, barrierPID)
	go func() {
		_, e := publisher.PublishMavenProtocolAssets(ctx, session.ID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: "org/example/other/1.0-SNAPSHOT/other-1.0-SNAPSHOT.jar", ObjectKey: shared.ObjectKey, Digest: shared.Digest, Size: shared.Size}})
		publishDone <- e
	}()
	waitBlocked("publish-"+label, 0)
	if err = barrier.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, result := range []<-chan error{importDone, publishDone} {
		select {
		case err = <-result:
			if err != nil {
				t.Errorf("concurrent publication failed: %v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	checkpoint, err = importer.GetMavenSnapshotImport(ctx, repo.ID, plan.Coordinate)
	if err != nil || checkpoint.State != "committed" {
		t.Fatalf("import checkpoint=%+v %v", checkpoint, err)
	}
	if _, err = publisher.GetMavenArtifactByCoordinate(ctx, repo.ID, session.Coordinate); err != nil {
		t.Fatal(err)
	}
}

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
	// Match the production tombstone's quota-before-artifact order.
	if _, err = tx.ExecContext(ctx, `SELECT repository_id FROM repository_capacity_quotas WHERE repository_id=$1 FOR UPDATE`, repo.ID); err != nil {
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
		if err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND (query LIKE '%native_maven_artifacts%' OR query LIKE '%repository_capacity_quotas%'))`, pid).Scan(&blocked); err != nil {
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
