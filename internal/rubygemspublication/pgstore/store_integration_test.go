//go:build integration

package pgstore_test

import (
	"os"
	"testing"

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
