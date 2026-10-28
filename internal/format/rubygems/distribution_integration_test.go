//go:build integration

package rubygems

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/database"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	dist "github.com/ccsert/artifact-gateway/internal/rubygemsdistribution"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
	"github.com/ccsert/artifact-gateway/internal/rubygemspublication/pgstore"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
)

func distributionPostgres(t *testing.T) (distributionFixture, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("owned PostgreSQL and RustFS required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(dsn); err != nil {
		t.Fatal(err)
	}
	db, err := database.OpenPostgres(dsn, database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	locks, err := database.OpenPostgres(dsn, database.DefaultArtifactLockPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locks.Close() })
	repos, err := repository.NewPostgresStore(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	store, err := pgstore.New(db, locks)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "ruby-distribution-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	var source, target repository.HostedRepository
	for _, r := range []*repository.HostedRepository{&source, &target} {
		v, e := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "replication-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
		if e != nil {
			t.Fatal(e)
		}
		*r = v
	}
	auth := authorization.Authenticator{AdminToken: "owned-admin", AdminActor: "operator", APIKeys: repos, Users: repos, UserSessions: repos, ServiceAccounts: repos}
	m, err := New(Options{Store: store, Distribution: store, Repositories: repos, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: repos, Legacy: auth}, Audit: repos, Anonymous: repos})
	if err != nil {
		t.Fatal(err)
	}
	return distributionFixture{m, store, repos, objects, source, target}, db
}
func TestRubyGemsPostgresRustFSReplicationOriginalBytesAndCurrentAuthority(t *testing.T) {
	f, _ := distributionPostgres(t)
	testDistributionComplete(t, f)
	testDistributionGovernanceAndAuthority(t, f)
}
func TestRubyGemsPostgresRustFSReplicationAuditRollbackAndLeaseFenceAfterWait(t *testing.T) {
	f, db := distributionPostgres(t)
	p, _ := distributionPublish(t, f, f.source, "owned_audit", "ruby")
	j := distributionCreate(t, f, distributionPlan(f, p), "owned-admin", uuid.NewString())
	trigger := "ruby_replication_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := db.ExecContext(t.Context(), `CREATE FUNCTION `+trigger+`() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF NEW.repository='`+f.target.Name+`' AND NEW.operation='rubygems.replication.item' AND NEW.http_status=200 THEN RAISE EXCEPTION 'owned replication audit failure'; END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS `+trigger+` ON resolver_audit_log;DROP FUNCTION IF EXISTS `+trigger+`()`)
	})
	if _, err = db.ExecContext(t.Context(), `CREATE TRIGGER `+trigger+` BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION `+trigger+`() `); err != nil {
		t.Fatal(err)
	}
	j = distributionRun(t, f, j.ID)
	if j.State != "failed" || j.Items[0].Code != "transfer_failed" {
		t.Fatalf("audit failure %+v", j)
	}
	if _, err = f.store.Get(t.Context(), f.target.ID, p.FullName()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("audit failure published", err)
	}
	audits, err := f.repos.ListAudits(t.Context(), repository.AuditQuery{Repository: f.target.Name, Operation: "rubygems.publish", Limit: 100})
	if err != nil || len(audits) != 0 {
		t.Fatal("publication audit escaped rollback", audits, err)
	}
	if _, err = db.ExecContext(t.Context(), `DROP TRIGGER `+trigger+` ON resolver_audit_log;DROP FUNCTION `+trigger+`() `); err != nil {
		t.Fatal(err)
	}
	persisted, err := f.store.GetRubyGemsReplication(t.Context(), j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = f.store.RetryRubyGemsReplication(t.Context(), j.ID, persisted.Version, []int{0}, uuid.NewString(), f.module.replicationCheck(persisted)); err != nil {
		t.Fatal(err)
	}
	j = distributionRun(t, f, j.ID)
	if j.State != "succeeded" || j.Items[0].Attempts != 2 {
		t.Fatalf("audit recovery %+v", j)
	}
	// Hold the source row after the worker has acquired its job/authority locks.
	// Expiry while this SELECT is blocked must roll back the whole publication.
	p, _ = distributionPublish(t, f, f.source, "owned_lease", "ruby")
	j = distributionCreate(t, f, distributionPlan(f, p), "owned-admin", uuid.NewString())
	claim, err := f.store.ClaimRubyGemsReplication(t.Context(), "old-owner", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := f.store.Reserve(t.Context(), domain.Session{ID: uuid.NewString(), RepositoryID: f.target.ID, Name: p.Name, Version: p.Version, Platform: p.Platform, Digest: p.Digest, Size: p.Size, Actor: j.Actor, MetadataJSON: p.MetadataJSON, MetadataYAML: p.MetadataYAML, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	check := f.module.replicationCheck(claim)
	if _, err = f.store.BindRubyGemsReplicationSession(t.Context(), claim.Lease, 0, session.ID, check); err != nil {
		t.Fatal(err)
	}
	blocker, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err = blocker.ExecContext(t.Context(), `SELECT id FROM native_rubygems_sessions WHERE id::text=$1 FOR UPDATE`, p.SessionID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, e := f.store.CompleteRubyGemsReplicationItem(t.Context(), claim.Lease, 0, dist.Commit{SessionID: session.ID}, check)
		result <- e
	}()
	deadline := time.Now().Add(5 * time.Second)
	observed := false
	for time.Now().Before(deadline) {
		if err = db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%native_rubygems_sessions%FOR SHARE%')`).Scan(&observed); err != nil {
			t.Fatal(err)
		}
		if observed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !observed {
		t.Fatal("publication never waited on the source row")
	}
	if delay := time.Until(claim.Lease.Until) + 20*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	if err = blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-result:
		if !errors.Is(err, dist.ErrLeaseLost) {
			t.Fatalf("late lease commit %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not finish")
	}
	if _, err = f.store.Get(t.Context(), f.target.ID, p.FullName()); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("expired owner published", err)
	}
	replacement, err := f.store.ClaimRubyGemsReplication(t.Context(), "replacement", time.Minute)
	if err != nil || replacement.Lease.Fence <= claim.Lease.Fence {
		t.Fatalf("replacement %+v %v", replacement, err)
	}
	if err = f.module.ReplicateRubyGemsItem(t.Context(), replacement, replacement.Items[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.FinishRubyGemsReplication(t.Context(), replacement.Lease); err != nil {
		t.Fatal(err)
	}
}

func TestRubyGemsPostgresRustFSReplicationSurvivesSimulatedSourceObjectRemoval(t *testing.T) {
	f, _ := distributionPostgres(t)
	testDistributionSourceRemoval(t, f)
}
