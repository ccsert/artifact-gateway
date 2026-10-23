//go:build integration

package rubygems

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
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
	otherDB.SetMaxOpenConns(1)
	otherDB.SetMaxIdleConns(1)
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
	t.Run("revoked-key-during-transaction-lock-wait", func(t *testing.T) {
		token := "owned-row-wait-key"
		key, err := repos.CreateAPIKey(t.Context(), repository.APIKey{ID: uuid.NewString(), Name: "owned-row-wait", SecretHash: authorization.HashAPIKey(token), Roles: []string{"member"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = repos.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "api-key:" + key.ID, Scopes: []string{"repositories:write"}, ResourcePrefix: "durable"}}, "1"); err != nil {
			t.Fatal(err)
		}
		auth.APIKeys = repos
		m = compose()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		block, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = block.Rollback() }()
		if _, err = block.ExecContext(ctx, `SELECT id FROM native_rubygems_sessions WHERE id::text=$1 FOR UPDATE`, p.SessionID); err != nil {
			t.Fatal(err)
		}
		result := make(chan int, 1)
		go func() {
			r := httptest.NewRequest("DELETE", "/rubygems/"+repo.Name+"/api/v1/gems/yank", strings.NewReader("gem_name=durable&version=1.0.0")).WithContext(ctx)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Authorization", token)
			w := httptest.NewRecorder()
			m.ServeHTTP(w, r)
			result <- w.Code
		}()
		defer func() {
			cancel()
			_ = block.Rollback()
			select {
			case <-result:
			case <-time.After(2 * time.Second):
			}
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var waiting int
			err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%native_rubygems_sessions%FOR UPDATE%'`).Scan(&waiting)
			if err != nil {
				t.Fatal(err)
			}
			if waiting > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("request did not reach PostgreSQL row-lock wait")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if _, err = repos.RevokeAPIKey(ctx, key.ID); err != nil {
			t.Fatal(err)
		}
		if err = block.Rollback(); err != nil {
			t.Fatal(err)
		}
		select {
		case code := <-result:
			if code != 403 {
				t.Fatalf("revoked key passed commit boundary: %d", code)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		got, err := store.GetRetained(t.Context(), repo.ID, "durable", "1.0.0", "ruby")
		if err != nil || got.Governance.Yanked || got.Governance.Version != "3" {
			t.Fatal(got, err)
		}
	})
	for _, action := range []string{"yank", "restore"} {
		t.Run("expired-administrator-protocol-token-during-transaction-lock-wait/"+action, func(t *testing.T) {
			if otherDB.Stats().MaxOpenConnections != 1 {
				t.Fatal("publication pool must remain constrained to one connection")
			}
			if action == "restore" {
				if w := yankRequest(m, repo.Name, "durable", "1.0.0", "", "owned-admin"); w.Code != 200 {
					t.Fatal(w.Code)
				}
			}
			before, err := store.GetRetained(t.Context(), repo.ID, "durable", "1.0.0", "ruby")
			if err != nil {
				t.Fatal(err)
			}
			priorAudits, err := repos.ListAudits(t.Context(), repository.AuditQuery{Repository: repo.Name, Operation: "rubygems." + action, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			auth.ResolverToken = "owned-governance-protocol-signer"
			m = compose()
			expiry := time.Now().Add(3 * time.Second).Truncate(time.Second)
			token := ownedExpiringRubyGemsPrincipalToken(t, auth, authorization.Principal{Actor: "operator", Admin: true, Role: authorization.RoleAdmin, AuthenticationKind: authorization.AuthenticationStaticAdmin}, expiry)
			if principal, ok := auth.AuthenticateContext(t.Context(), "Bearer "+token); !ok || !principal.Admin {
				t.Fatal("owned protocol token must authenticate before row-lock wait")
			}
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			var requestPID int
			if err = otherDB.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&requestPID); err != nil {
				t.Fatal(err)
			}
			block, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = block.Rollback() }()
			if _, err = block.ExecContext(ctx, `SELECT id FROM native_rubygems_sessions WHERE id::text=$1 FOR UPDATE`, p.SessionID); err != nil {
				t.Fatal(err)
			}
			result, done := make(chan int, 1), make(chan struct{})
			go func() {
				defer close(done)
				var request *httptest.ResponseRecorder
				if action == "yank" {
					r := httptest.NewRequest("DELETE", "/rubygems/"+repo.Name+"/api/v1/gems/yank", strings.NewReader("gem_name=durable&version=1.0.0")).WithContext(ctx)
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					r.Header.Set("Authorization", token)
					request = httptest.NewRecorder()
					m.ServeHTTP(request, r)
				} else {
					body, _ := json.Marshal(map[string]string{"expectedDigest": before.Digest, "expectedGovernanceVersion": before.Governance.Version, "action": "restore", "reason": "Owned expiring administrator protocol credential"})
					r := httptest.NewRequest("POST", governancePath(repo.ID, "durable", "1.0.0", "ruby"), bytes.NewReader(body)).WithContext(ctx)
					r.Header.Set("Content-Type", "application/json")
					r.Header.Set("Authorization", "Bearer "+token)
					request = httptest.NewRecorder()
					m.Preview().ServeHTTP(request, r)
				}
				result <- request.Code
			}()
			defer func() {
				cancel()
				_ = block.Rollback()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("owned request did not finish cleanup")
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var waiting bool
				err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock' AND query LIKE '%native_rubygems_sessions%FOR UPDATE%')`, requestPID).Scan(&waiting)
				if err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case code := <-result:
					t.Fatalf("request finished before transaction wait: %d", code)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("request never waited on the owned transaction row lock")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !time.Now().Before(expiry) {
				t.Fatal("protocol token expired before the observed transaction wait")
			}
			timer := time.NewTimer(time.Until(expiry) + 25*time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if _, ok := auth.AuthenticateContext(ctx, "Bearer "+token); ok {
				t.Fatal("fixture protocol token is still accepted after signed expiry")
			}
			if err = block.Rollback(); err != nil {
				t.Fatal(err)
			}
			select {
			case code := <-result:
				if code != 403 {
					t.Fatalf("expired administrator protocol token passed commit boundary: %d", code)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			got, err := store.GetRetained(t.Context(), repo.ID, "durable", "1.0.0", "ruby")
			if err != nil || got.Governance != before.Governance {
				t.Fatalf("expired token changed state: %+v %v", got, err)
			}
			audits, err := repos.ListAudits(t.Context(), repository.AuditQuery{Repository: repo.Name, Operation: "rubygems." + action, Limit: 100})
			if err != nil || len(audits) != len(priorAudits) {
				t.Fatalf("expired token changed audit: %d -> %d %v", len(priorAudits), len(audits), err)
			}
		})
	}
}
