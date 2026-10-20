//go:build integration

package app

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/ccsert/artifact-gateway/internal/testsupport/ocifixture"
	"github.com/google/uuid"
)

type ownedNexusOCIStore struct {
	*repository.PostgresStore
	db   *sql.DB
	mu   sync.Mutex
	jobs map[string]string
}

func newNexusOCIIntegrationFixture(t *testing.T) (*ownedNexusOCIStore, *objectstore.RustFSStore) {
	t.Helper()
	connection, endpoint := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_RUSTFS_ENDPOINT")
	if connection == "" || endpoint == "" {
		t.Skip("owned PostgreSQL and RustFS required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(connection); err != nil {
		t.Fatal(err)
	}
	native, err := repository.NewPostgresStore(connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = native.Close() })
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	objects, err := objectstore.NewRustFSStore(endpoint, os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "nexus-oci-"+strings.ReplaceAll(uuid.NewString(), "-", "")[:20])
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &ownedNexusOCIStore{PostgresStore: native, db: db, jobs: map[string]string{}}, objects
}

func (s *ownedNexusOCIStore) EnqueueLifecycleJob(ctx context.Context, job repository.LifecycleJob) (repository.LifecycleJob, bool, error) {
	stored, created, err := s.PostgresStore.EnqueueLifecycleJob(ctx, job)
	if err == nil {
		s.mu.Lock()
		s.jobs[stored.ID] = stored.RepositoryID
		s.mu.Unlock()
	}
	return stored, created, err
}

// Restrict actual SQL lease claiming to fixture-owned UUIDs. The worker still
// uses production object locking, deletion, finalization and job completion.
func (s *ownedNexusOCIStore) ClaimLifecycleJobsByKindAndFormat(ctx context.Context, kind repository.LifecycleJobKind, format repository.Format, limit int) ([]repository.LifecycleJob, error) {
	s.mu.Lock()
	jobs := make(map[string]string, len(s.jobs))
	for id, repo := range s.jobs {
		jobs[id] = repo
	}
	s.mu.Unlock()
	claimed := []repository.LifecycleJob{}
	for id, repo := range jobs {
		if len(claimed) >= limit {
			break
		}
		result, err := s.db.ExecContext(ctx, `UPDATE lifecycle_jobs jobs SET state='running',started_at=now(),completed_at=NULL,next_attempt_at=NULL,lease_expires_at=now()+interval '10 minutes',lease_token=gen_random_uuid()::text,attempts=attempts+1 WHERE id=$1 AND repository_id=$2 AND kind=$3 AND payload->>'format'=$4 AND state IN ('pending','retrying') AND COALESCE(next_attempt_at,created_at)<=now() AND attempts<max_attempts AND NOT EXISTS(SELECT 1 FROM lifecycle_jobs running WHERE running.repository_id=jobs.repository_id AND running.state='running')`, id, repo, kind, format)
		if err != nil {
			return nil, err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			continue
		}
		job, err := s.GetLifecycleJob(ctx, repo, id)
		if err != nil {
			return nil, err
		}
		claimed = append(claimed, job)
	}
	return claimed, nil
}

func TestPostgresRustFSNexusOCICompletePublication(t *testing.T) {
	store, objects := newNexusOCIIntegrationFixture(t)
	exerciseNexusOCICompletePublication(t, store, objects)
}

func TestPostgresRustFSNexusOCIGarbageCollectionAndImportSerialize(t *testing.T) {
	for _, mode := range []string{"collector_wins", "publication_wins", "published_stage_failure"} {
		t.Run(mode, func(t *testing.T) {
			store, objects := newNexusOCIIntegrationFixture(t)
			exerciseNexusOCIGarbageCollection(t, store, objects, mode)
		})
	}
}

func TestPostgresRustFSNexusOCIPublicationFenceAuditAndRestart(t *testing.T) {
	store, objects := newNexusOCIIntegrationFixture(t)
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "oci-atomic-" + uuid.NewString()[:8], Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	f := ocifixture.New(t.Name() + uuid.NewString())
	e := nexusOCIInventory(repo, f)
	p := NewNexusOCIMigrationPublication(store, objects)
	locked, release, err := p.Lock(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	nexusOCIStage(t, locked, p, e, f)
	if err = p.Verify(locked, e); err != nil {
		t.Fatal(err)
	}
	guarded := repository.WithNativePublicationGuard(locked, func(context.Context, *sql.Tx) error { return errors.New("synthetic expired batch fence") })
	if err = p.Publish(guarded, e); err == nil {
		t.Fatal("expired batch owner published native closure")
	}
	if state, err := p.Readback(ctx, e); err != nil || state != nexusmigration.Absent {
		t.Fatalf("failed owner was visible: %s %v", state, err)
	}
	trigger := "nexus_oci_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS `+trigger+` ON resolver_audit_log`)
		_, _ = store.db.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS `+trigger+`()`)
	})
	if _, err = store.db.ExecContext(ctx, `CREATE FUNCTION `+trigger+`()RETURNS trigger AS $$BEGIN IF NEW.repository='`+repo.Name+`' AND NEW.operation='nexus.migration.publish' THEN RAISE EXCEPTION 'synthetic OCI audit unavailable'; END IF; RETURN NEW; END;$$ LANGUAGE plpgsql`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.ExecContext(ctx, `CREATE TRIGGER `+trigger+` BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION `+trigger+`() `); err != nil {
		t.Fatal(err)
	}
	if err = p.Publish(locked, e); err == nil {
		t.Fatal("audit failure committed closure")
	}
	if state, err := p.Readback(ctx, e); err != nil || state != nexusmigration.Absent {
		t.Fatalf("failed audit was visible: %s %v", state, err)
	}
	if _, err = store.db.ExecContext(ctx, `DROP TRIGGER `+trigger+` ON resolver_audit_log`); err != nil {
		t.Fatal(err)
	}
	if err = p.Publish(locked, e); err != nil {
		t.Fatal(err)
	}
	if err = p.Publish(locked, e); err != nil {
		t.Fatalf("commit response loss replay=%v", err)
	}
	var audits int
	if err = store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM resolver_audit_log WHERE repository=$1 AND actor=$2 AND operation='nexus.migration.publish'`, repo.Name, e.Actor).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("atomic actual-actor publication audit=%d %v", audits, err)
	}
	release()
	if err = store.PostgresStore.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := repository.NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	fresh := NewNexusOCIMigrationPublication(reopened, objects)
	if state, err := fresh.Readback(ctx, e); err != nil || state != nexusmigration.Identical {
		t.Fatalf("reopened native closure=%s %v", state, err)
	}
	var manifestCount, blobCount int
	if err = store.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM native_oci_manifests WHERE repository_id=$1),(SELECT COUNT(*) FROM native_oci_repository_blobs WHERE repository_id=$1)`, repo.ID).Scan(&manifestCount, &blobCount); err != nil || manifestCount != 3 || blobCount != 3 {
		t.Fatalf("persisted closure manifest=%d blob=%d %v", manifestCount, blobCount, err)
	}
}
