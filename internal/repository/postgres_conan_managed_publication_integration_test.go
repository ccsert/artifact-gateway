//go:build integration

package repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresConanManagedPublication(t *testing.T) {
	store := openPostgresConanManagedContractStore(t)
	exerciseConanManagedPublication(t, store, func(t *testing.T, id string) {
		t.Helper()
		if _, err := store.db.Exec(`UPDATE native_conan_publish_sessions SET expires_at=$2 WHERE id=$1`, id, time.Now().Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPostgresConanManagedPublicationAuditFailureRollsBack(t *testing.T) {
	store := openPostgresConanManagedContractStore(t)
	ctx := context.Background()
	for _, kind := range []string{"recipe", "package"} {
		t.Run(kind, func(t *testing.T) {
			f := newConanManagedContractFixture(t, store, kind)
			f.stageAll(t)
			f.assertGCEligibility(t, false)
			removeTrigger := installConanManagedContractAuditFailure(t, store, f.repo.Name, f.session.ID)
			_, _, err := store.CommitConanPublicationSession(ctx, f.session.ID, f.session.Version, f.digest(), "actual-committer")
			if err == nil || !strings.Contains(err.Error(), "fixture managed Conan audit unavailable") {
				t.Fatalf("commit did not reach the fixture audit failure: %v", err)
			}
			f.assertUnpublished(t)
			f.assertGCEligibility(t, false)

			// The installed trigger cannot reject another repository, even when
			// its publication uses the same actor and protocol mode.
			control := newConanManagedContractFixture(t, store, kind)
			control.stageAll(t)
			control.commit(t, "actual-committer")

			// A retry of this exact snapshot succeeds only if asset claims,
			// revision metadata and the session update all rolled back.
			removeTrigger()
			f.commit(t, "actual-committer")
		})
	}
}

func openPostgresConanManagedContractStore(t *testing.T) *PostgresStore {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func installConanManagedContractAuditFailure(t *testing.T, store *PostgresStore, repositoryName, sessionID string) func() {
	t.Helper()
	// Names and arguments come only from UUID-owned fixture resources. Each
	// cleanup removes exactly this test's trigger and function, without CASCADE.
	name := "conan_managed_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	remove := func() {
		t.Helper()
		if _, err := store.db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON resolver_audit_log`, name)); err != nil {
			t.Errorf("remove owned audit trigger: %v", err)
		}
		if _, err := store.db.Exec(fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, name)); err != nil {
			t.Errorf("remove owned audit function: %v", err)
		}
	}
	t.Cleanup(remove)
	_, err := store.db.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.repository=TG_ARGV[0] AND NEW.operation='publish' AND NEW.evidence->>'publicationSessionId'=TG_ARGV[1] THEN
        RAISE EXCEPTION 'fixture managed Conan audit unavailable';
    END IF;
    RETURN NEW;
END;
$$`, name))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION %s('%s','%s')`, name, name, repositoryName, sessionID))
	if err != nil {
		t.Fatal(err)
	}
	return remove
}
