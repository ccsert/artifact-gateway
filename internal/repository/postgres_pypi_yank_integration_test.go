//go:build integration

package repository

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func openPostgresPyPIYank(t *testing.T) (*PostgresStore, HostedRepository) {
	t.Helper()
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.CreateHostedRepository(t.Context(), HostedRepository{ID: uuid.NewString(), Name: "yank-" + uuid.NewString()[:8], Format: FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	return store, repo
}

func TestPyPIYankPostgresContract(t *testing.T) {
	runPyPIYankContract(t, func(t *testing.T) (pypiYankContractStore, HostedRepository) { return openPostgresPyPIYank(t) })
}

func TestPyPIYankPostgresCrossInstanceAndAuditRollback(t *testing.T) {
	first, repo := openPostgresPyPIYank(t)
	second, err := NewPostgresStore(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	file, err := first.PublishPyPIFile(t.Context(), PyPIFile{RepositoryID: repo.ID, Project: "widget", Version: "1.0", Filename: "widget-1.0.tar.gz", Digest: "sha256:" + strings.Repeat("a", 64), ObjectKey: "test/pypi", Size: 42})
	if err != nil {
		t.Fatal(err)
	}
	mutation := PyPIYankMutation{RepositoryID: repo.ID, Project: "widget", Version: "1.0", Yanked: true, ExpectedRevision: PyPIYankRevision([]PyPIFile{file}), Audit: AuditRecord{Repository: repo.Name, Actor: "writer", Format: "pypi", Operation: "pypi.yank", Outcome: AuditResolved, Status: 200}}
	var wg sync.WaitGroup
	errorsFound := make(chan error, 2)
	for i, store := range []*PostgresStore{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := mutation
			m.Reason = string(rune('a' + i))
			_, changeErr := store.SetPyPIYank(t.Context(), m)
			errorsFound <- changeErr
		}()
	}
	wg.Wait()
	close(errorsFound)
	committed, stale := 0, 0
	for err := range errorsFound {
		if err == nil {
			committed++
		} else if errors.Is(err, ErrVersionConflict) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if committed != 1 || stale != 1 {
		t.Fatalf("cross-instance committed=%d stale=%d", committed, stale)
	}
	files, err := second.ListPyPIProjectFiles(t.Context(), repo.ID, "widget")
	if err != nil || len(files) != 1 || !files[0].Yanked {
		t.Fatalf("cross-instance state=%+v %v", files, err)
	}
	before := PyPIYankRevision(files)
	mutation.ExpectedRevision = before
	mutation.Yanked = false
	mutation.Audit.Evidence = map[string]string{"rollback": "\x00"}
	// PostgreSQL jsonb rejects a NUL in the audit evidence. The already-issued
	// metadata UPDATE must roll back with that failed audit insert.
	if _, err = second.SetPyPIYank(t.Context(), mutation); err == nil {
		t.Fatal("invalid audit unexpectedly committed")
	}
	files, err = first.ListPyPIProjectFiles(t.Context(), repo.ID, "widget")
	if err != nil || PyPIYankRevision(files) != before {
		t.Fatalf("failed audit changed state=%+v %v", files, err)
	}
	var count int
	if err = first.db.QueryRowContext(t.Context(), `SELECT count(*) FROM resolver_audit_log WHERE repository=$1 AND operation='pypi.yank'`, repo.Name).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audits=%d %v", count, err)
	}
}
