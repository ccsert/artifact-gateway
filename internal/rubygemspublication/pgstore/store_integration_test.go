//go:build integration

package pgstore_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/database"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/rubygemspublication/pgstore"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	f "github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
	"github.com/google/uuid"
)

func TestRubyGemsPostgresPublicationStoreContract(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("owned PostgreSQL required")
	}
	if e := testsupport.ValidateIsolatedDatabaseURL(dsn); e != nil {
		t.Fatal(e)
	}
	db, e := database.OpenPostgres(dsn, database.DefaultPoolConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	locks, e := database.OpenPostgres(dsn, database.DefaultArtifactLockPoolConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = locks.Close() }()
	repos, e := repository.NewPostgresStore(dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = repos.Close() }()
	repo, e := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "ruby-contract-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
	if e != nil {
		t.Fatal(e)
	}
	store, e := pgstore.New(db, locks)
	if e != nil {
		t.Fatal(e)
	}
	f.StoreContract(t, store, repo)
}

func TestRubyGemsPostgresGovernanceStoreContract(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("owned PostgreSQL required")
	}
	if e := testsupport.ValidateIsolatedDatabaseURL(dsn); e != nil {
		t.Fatal(e)
	}
	db, e := database.OpenPostgres(dsn, database.DefaultPoolConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = db.Close() }()
	// Authority reads and CAS must finish with the publication pool restricted to one connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	locks, e := database.OpenPostgres(dsn, database.DefaultArtifactLockPoolConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = locks.Close() }()
	repos, e := repository.NewPostgresStore(dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = repos.Close() }()
	repo, e := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "ruby-contract-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
	if e != nil {
		t.Fatal(e)
	}
	store, e := pgstore.New(db, locks)
	if e != nil {
		t.Fatal(e)
	}
	f.GovernanceContract(t, store, repo)
}

func TestRubyGemsPostgresReplicationStoreContract(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("owned PostgreSQL required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(dsn); err != nil {
		t.Fatal(err)
	}
	db, err := database.OpenPostgres(dsn, database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	locks, err := database.OpenPostgres(dsn, database.DefaultArtifactLockPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locks.Close() }()
	repos, err := repository.NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repos.Close() }()
	store, err := pgstore.New(db, locks)
	if err != nil {
		t.Fatal(err)
	}
	var source, target repository.HostedRepository
	for _, r := range []*repository.HostedRepository{&source, &target} {
		v, err := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "replication-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
		if err != nil {
			t.Fatal(err)
		}
		*r = v
	}
	f.ReplicationStoreContract(t, store, source, target)
}

func TestRubyGemsPostgresReplicationRejectsPublicationExpiryAfterAudit(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("owned PostgreSQL required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(dsn); err != nil {
		t.Fatal(err)
	}
	db, err := database.OpenPostgres(dsn, database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	locks, err := database.OpenPostgres(dsn, database.DefaultArtifactLockPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locks.Close() }()
	repos, err := repository.NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repos.Close() }()
	store, err := pgstore.New(db, locks)
	if err != nil {
		t.Fatal(err)
	}
	var source, target repository.HostedRepository
	for _, r := range []*repository.HostedRepository{&source, &target} {
		v, err := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "publication-expiry-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
		if err != nil {
			t.Fatal(err)
		}
		*r = v
	}
	f.ReplicationPublicationExpiryContract(t, store, repos, source, target, func(expiry time.Time, complete func() error) error {
		probe := "ruby_expiry_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		sequence := probe + "_entered"
		query := `CREATE SEQUENCE ` + sequence + `;CREATE FUNCTION ` + probe + `() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF NEW.repository='` + target.Name + `' AND NEW.operation='rubygems.replication.item' AND NEW.http_status=200 THEN PERFORM nextval('` + sequence + `');PERFORM pg_sleep(GREATEST(0,EXTRACT(EPOCH FROM ('` + expiry.Format(time.RFC3339Nano) + `'::timestamptz-clock_timestamp())))+0.03);END IF;RETURN NEW;END $$;CREATE TRIGGER ` + probe + ` BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION ` + probe + `() `
		if _, err := db.ExecContext(t.Context(), query); err != nil {
			t.Fatal(err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := db.ExecContext(ctx, `DROP TRIGGER IF EXISTS `+probe+` ON resolver_audit_log;DROP FUNCTION IF EXISTS `+probe+`();DROP SEQUENCE IF EXISTS `+sequence); err != nil {
				t.Error(err)
			}
		}()
		err := complete()
		var entered bool
		if e := db.QueryRowContext(t.Context(), `SELECT is_called FROM `+sequence).Scan(&entered); e != nil || !entered {
			t.Fatalf("PG audit did not enter before expiry: %t %v", entered, e)
		}
		return err
	})
}
