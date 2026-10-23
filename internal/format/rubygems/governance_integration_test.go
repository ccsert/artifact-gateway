//go:build integration

package rubygems

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/authorization"
	"github.com/ccsert/artifact-gateway/internal/database"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/rubygemspublication/pgstore"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
)

func TestRubyGemsPostgresRustFSGovernanceAtomicAuditAndRestart(t *testing.T) {
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
	objects, err := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "ruby-governance-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.EnsureBucket(t.Context()); err != nil {
		t.Fatal(err)
	}
	repo, err := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "ruby-governance-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
	if err != nil {
		t.Fatal(err)
	}
	auth := authorization.Authenticator{AdminToken: "owned-admin", AdminActor: "operator"}
	compose := func() *Module {
		m, err := New(Options{Store: store, Repositories: repos, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: repos, Legacy: auth}, Audit: repos, Anonymous: repos})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := compose()
	body := gem(t, "durable", "1.0.0", "ruby", "fixture")
	if w := request(m, "POST", "/rubygems/"+repo.Name+"/api/v1/gems", body, "owned-admin"); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, err := store.GetRetained(t.Context(), repo.ID, "durable", "1.0.0", "ruby")
	if err != nil {
		t.Fatal(err)
	}
	// The owned trigger fails both state transitions at the same audit insert
	// used in production; neither metadata nor a successful audit may escape.
	trigger := "ruby_governance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.ExecContext(t.Context(), `CREATE FUNCTION `+trigger+`() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF NEW.repository='`+repo.Name+`' AND NEW.operation IN ('rubygems.yank','rubygems.restore') THEN RAISE EXCEPTION 'owned governance audit failure'; END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(t.Context(), `DROP TRIGGER IF EXISTS `+trigger+` ON resolver_audit_log`)
		_, _ = db.ExecContext(t.Context(), `DROP FUNCTION IF EXISTS `+trigger+`()`)
	}()
	enableFailure := func() {
		_, err := db.ExecContext(t.Context(), `CREATE TRIGGER `+trigger+` BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION `+trigger+`()`)
		if err != nil {
			t.Fatal(err)
		}
	}
	disableFailure := func() {
		_, err := db.ExecContext(t.Context(), `DROP TRIGGER `+trigger+` ON resolver_audit_log`)
		if err != nil {
			t.Fatal(err)
		}
	}
	enableFailure()
	if w := yankRequest(m, repo.Name, "durable", "1.0.0", "", "owned-admin"); w.Code != 500 {
		t.Fatal(w.Code)
	}
	before, err := store.GetRetained(t.Context(), repo.ID, "durable", "1.0.0", "ruby")
	if err != nil || before.Governance.Yanked || before.Governance.Version != "1" {
		t.Fatal(before, err)
	}
	disableFailure()
	for range 2 {
		if w := yankRequest(m, repo.Name, "durable", "1.0.0", "", "owned-admin"); w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	enableFailure()
	governanceChange(t, m.Preview(), repo.ID, "durable", "1.0.0", "ruby", p.Digest, "2", "restore", "Bearer owned-admin", 500)
	disableFailure()
	capacity, err := store.Capacity(t.Context(), repo.ID)
	if err != nil || capacity.UsedBytes != p.Size || capacity.ObjectCount != 1 {
		t.Fatal(capacity, err)
	}
	maintenance, _ := NewMaintenance(store, objects)
	if err = maintenance.Collect(t.Context(), time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = objects.Delete(t.Context(), p.ObjectKey); err != nil {
		t.Fatal(err)
	}
	governanceChange(t, m.Preview(), repo.ID, "durable", "1.0.0", "ruby", p.Digest, "2", "restore", "Bearer owned-admin", 500)
	if err = objects.Put(t.Context(), p.ObjectKey, []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	governanceChange(t, m.Preview(), repo.ID, "durable", "1.0.0", "ruby", p.Digest, "2", "restore", "Bearer owned-admin", 500)
	if err = objects.Put(t.Context(), p.ObjectKey, body); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(t.Context(), `UPDATE native_rubygems_sessions SET metadata_json='{}' WHERE id::text=$1`, p.SessionID); err != nil {
		t.Fatal(err)
	}
	governanceChange(t, m.Preview(), repo.ID, "durable", "1.0.0", "ruby", p.Digest, "2", "restore", "Bearer owned-admin", 500)
	if _, err = db.ExecContext(t.Context(), `UPDATE native_rubygems_sessions SET metadata_json=$2 WHERE id::text=$1`, p.SessionID, string(p.MetadataJSON)); err != nil {
		t.Fatal(err)
	}
	otherDB, err := database.OpenPostgres(dsn, database.DefaultPoolConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = otherDB.Close() }()
	store, err = pgstore.New(otherDB, locks)
	if err != nil {
		t.Fatal(err)
	}
	m = compose()
	retained, err := store.GetRetained(t.Context(), repo.ID, "durable", "1.0.0", "ruby")
	if err != nil || !retained.Governance.Yanked || retained.Governance.Version != "2" {
		t.Fatal(retained, err)
	}
	if w := request(m, "GET", "/rubygems/"+repo.Name+"/gems/durable-1.0.0.gem", nil, "owned-admin"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	governanceChange(t, m.Preview(), repo.ID, "durable", "1.0.0", "ruby", p.Digest, "2", "restore", "Bearer owned-admin", 200)
	w := request(m, "GET", "/rubygems/"+repo.Name+"/gems/durable-1.0.0.gem", nil, "owned-admin")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatal(w.Code)
	}
	for _, op := range []string{"rubygems.yank", "rubygems.restore"} {
		rows, err := repos.ListAudits(t.Context(), repository.AuditQuery{Repository: repo.Name, Operation: op, Limit: 100})
		if err != nil || len(rows) != 1 || rows[0].Evidence["digest"] != p.Digest {
			t.Fatal(rows, err)
		}
	}
}
