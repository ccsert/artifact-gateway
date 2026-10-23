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

func TestRubyGemsPostgresRustFSAtomicAuditInterruptionQuotaAndRestart(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" || os.Getenv("TEST_RUSTFS_ENDPOINT") == "" {
		t.Skip("owned PostgreSQL and RustFS required")
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
	store, e := pgstore.New(db, locks)
	if e != nil {
		t.Fatal(e)
	}
	objects, e := objectstore.NewRustFSStore(os.Getenv("TEST_RUSTFS_ENDPOINT"), os.Getenv("TEST_RUSTFS_ACCESS_KEY"), os.Getenv("TEST_RUSTFS_SECRET_KEY"), "ruby-integrity-"+uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = objects.EnsureBucket(t.Context()); e != nil {
		t.Fatal(e)
	}
	repo, e := repos.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "ruby-integrity-" + uuid.NewString(), Format: repository.FormatRubyGems, Type: repository.RepositoryTypeHosted})
	if e != nil {
		t.Fatal(e)
	}
	auth := authorization.Authenticator{AdminToken: "owned-admin", AdminActor: "operator"}
	compose := func(objects objectstore.Store) *Module {
		m, e := New(Options{Store: store, Repositories: repos, Objects: objects, Authenticator: auth, Authorizer: authorization.RepositoryAuthorizer{Grants: repos, Legacy: auth}, Audit: repos, Anonymous: repos})
		if e != nil {
			t.Fatal(e)
		}
		return m
	}
	broken := &interrupted{Store: objects}
	broken.fail.Store(true)
	module := compose(broken)
	body := gem(t, "durable", "1.0.0", "ruby", "fixture")
	upload := func(m *Module) int {
		return request(m, "POST", "/rubygems/"+repo.Name+"/api/v1/gems", body, "owned-admin").Code
	}
	if code := upload(module); code != 500 {
		t.Fatal(code)
	}
	rows, e := store.List(t.Context(), repo.ID, 10)
	if e != nil || len(rows) != 0 {
		t.Fatalf("partial index %v %v", rows, e)
	}
	capacity, e := store.Capacity(t.Context(), repo.ID)
	if e != nil || capacity.PendingBytes != int64(len(body)) || capacity.UsedBytes != 0 {
		t.Fatalf("reservation %+v %v", capacity, e)
	}
	maintenance, _ := NewMaintenance(store, objects)
	if e = maintenance.Collect(t.Context(), time.Now().Add(2*time.Hour)); e != nil {
		t.Fatal(e)
	}
	keys, e := objects.List(t.Context(), "native/rubygems/"+repo.ID)
	if e != nil || len(keys) != 0 {
		t.Fatalf("owned expired objects %v %v", keys, e)
	}
	module = compose(objects)
	name := "ruby_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, e = db.ExecContext(t.Context(), `CREATE FUNCTION `+name+`() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF NEW.repository='`+repo.Name+`' AND NEW.operation='rubygems.publish' THEN RAISE EXCEPTION 'owned RubyGems audit failure'; END IF; RETURN NEW; END $$`)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.ExecContext(t.Context(), `CREATE TRIGGER `+name+` BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION `+name+`()`)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		_, _ = db.ExecContext(t.Context(), `DROP TRIGGER IF EXISTS `+name+` ON resolver_audit_log`)
		_, _ = db.ExecContext(t.Context(), `DROP FUNCTION IF EXISTS `+name+`()`)
	}()
	if code := upload(module); code != 500 {
		t.Fatal(code)
	}
	rows, e = store.List(t.Context(), repo.ID, 10)
	if e != nil || len(rows) != 0 {
		t.Fatalf("audit rollback left visible identity %v %v", rows, e)
	}
	audits, e := repos.ListAudits(t.Context(), repository.AuditQuery{Repository: repo.Name, Operation: "rubygems.publish", Limit: 10})
	if e != nil || len(audits) != 0 {
		t.Fatalf("failed transaction audit %v %v", audits, e)
	}
	if _, e = db.ExecContext(t.Context(), `DROP TRIGGER `+name+` ON resolver_audit_log`); e != nil {
		t.Fatal(e)
	}
	if code := upload(module); code != 201 {
		t.Fatal(code)
	}
	rows, e = store.List(t.Context(), repo.ID, 10)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	if _, e = store.SetQuota(t.Context(), repo.ID, "operator", int64(len(body))); e != nil {
		t.Fatal(e)
	}
	if code := request(module, "POST", "/rubygems/"+repo.Name+"/api/v1/gems", gem(t, "over-quota", "1.0.0", "ruby", "fixture"), "owned-admin").Code; code != 409 {
		t.Fatal(code)
	}
	// A separate adapter/pool sees the durable committed closure, independent
	// of the original module's in-memory state.
	otherDB, e := database.OpenPostgres(dsn, database.DefaultPoolConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = otherDB.Close() }()
	store, e = pgstore.New(otherDB, locks)
	if e != nil {
		t.Fatal(e)
	}
	module = compose(objects)
	w := request(module, "GET", "/rubygems/"+repo.Name+"/gems/durable-1.0.0.gem", nil, "owned-admin")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), body) {
		t.Fatal(w.Code)
	}
	corrupt := append([]byte(nil), body...)
	corrupt[len(corrupt)/2] ^= 1
	if e = objects.Put(t.Context(), rows[0].ObjectKey, corrupt); e != nil {
		t.Fatal(e)
	}
	w = request(module, "GET", "/rubygems/"+repo.Name+"/gems/durable-1.0.0.gem", nil, "owned-admin")
	if w.Code != 500 || bytes.Equal(w.Body.Bytes(), corrupt) {
		t.Fatal(w.Code)
	}
}
