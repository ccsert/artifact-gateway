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

func TestRubyGemsPostgresRustFSQuarantinePolicyAndRelease(t *testing.T) {
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

	quarantineReadContract(t, m, store, repo)
	quarantineAuthorityContract(t, m, store, repo, repos, repos)

	for _, action := range []string{"quarantine", "release", "policy"} {
		t.Run("postgres-transaction-key-revoked/"+action, func(t *testing.T) {
			before, err := store.GetRetained(t.Context(), repo.ID, "isolated", "1.0.0", "ruby")
			if err != nil {
				t.Fatal(err)
			}
			if action == "release" && !before.Governance.Quarantined {
				governanceChange(t, m.Preview(), repo.ID, before.Name, before.Version, before.Platform, before.Digest, before.Governance.Version, "quarantine", "Bearer owned-admin", 200)
				before, err = store.GetRetained(t.Context(), repo.ID, before.Name, before.Version, before.Platform)
				if err != nil {
					t.Fatal(err)
				}
			}
			token := uuid.NewString()
			key, err := repos.CreateAPIKey(t.Context(), repository.APIKey{ID: uuid.NewString(), Name: "tx-" + uuid.NewString(), SecretHash: authorization.HashAPIKey(token), Roles: []string{"member"}})
			if err != nil {
				t.Fatal(err)
			}
			grants, err := repos.GetRepositoryGrants(t.Context(), repo.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repos.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "api-key:" + key.ID, Scopes: []string{"repositories:admin"}}}, grants.Version); err != nil {
				t.Fatal(err)
			}
			current := *m
			current.auth.APIKeys = repos
			current.access.Legacy = current.auth
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			block, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = block.Rollback() }()
			table := "native_rubygems_sessions"
			query := `SELECT id FROM native_rubygems_sessions WHERE id::text=$1 FOR UPDATE`
			arg := before.SessionID
			path, method := governancePath(repo.ID, before.Name, before.Version, before.Platform), "POST"
			body, _ := json.Marshal(map[string]string{"expectedDigest": before.Digest, "expectedGovernanceVersion": before.Governance.Version, "action": action, "reason": "Owned PG transactional current authority"})
			policy, err := store.GetReadPolicy(ctx, repo.ID)
			if err != nil {
				t.Fatal(err)
			}
			if action == "policy" {
				table, query, arg = "repository_quarantine_read_policies", `SELECT repository_id FROM repository_quarantine_read_policies WHERE repository_id::text=$1 FOR UPDATE`, repo.ID
				path, method = policyPath(repo.ID), "PUT"
				body, _ = json.Marshal(map[string]any{"expectedVersion": policy.Version, "enabled": !policy.Enabled})
			}
			if _, err = block.ExecContext(ctx, query, arg); err != nil {
				t.Fatal(err)
			}
			result, done := make(chan int, 1), make(chan struct{})
			go func() {
				defer close(done)
				r := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(ctx)
				r.Header.Set("Authorization", "Bearer "+token)
				w := httptest.NewRecorder()
				current.Preview().ServeHTTP(w, r)
				result <- w.Code
			}()
			defer func() {
				cancel()
				_ = block.Rollback()
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("request cleanup timeout")
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				var waiting bool
				err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)`, "%"+table+"%FOR UPDATE%").Scan(&waiting)
				if err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("request did not reach PostgreSQL row lock")
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
					t.Fatalf("revoked PG authority passed %s: %d", action, code)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			after, err := store.GetRetained(ctx, repo.ID, before.Name, before.Version, before.Platform)
			if err != nil || after.Governance != before.Governance {
				t.Fatal("revoked PG governance committed", after, err)
			}
			afterPolicy, err := store.GetReadPolicy(ctx, repo.ID)
			if err != nil || afterPolicy != policy {
				t.Fatal("revoked PG policy committed", afterPolicy, err)
			}
		})
	}
	p, err := store.GetRetained(t.Context(), repo.ID, "isolated", "1.1.0.pre", "ruby")
	if err != nil {
		t.Fatal(err)
	}
	// The retained prerelease is quarantined even while the policy is disabled.
	for _, mode := range []string{"missing", "digest"} {
		if mode == "missing" {
			err = objects.Delete(t.Context(), p.ObjectKey)
		} else {
			err = objects.Put(t.Context(), p.ObjectKey, []byte("bad digest"))
		}
		if err != nil {
			t.Fatal(err)
		}
		governanceChange(t, m.Preview(), repo.ID, p.Name, p.Version, p.Platform, p.Digest, "2", "release", "Bearer owned-admin", 500)
		got, err := store.GetRetained(t.Context(), repo.ID, p.Name, p.Version, p.Platform)
		if err != nil || !got.Governance.Quarantined || got.Governance.Version != "2" {
			t.Fatal(got, err)
		}
	}
	if err = objects.Put(t.Context(), p.ObjectKey, gem(t, "isolated", "1.1.0.pre", "ruby", "owned")); err != nil {
		t.Fatal(err)
	}
	trigger := "ruby_quarantine_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = db.ExecContext(t.Context(), `CREATE FUNCTION `+trigger+`() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF NEW.repository='`+repo.Name+`' AND NEW.operation IN ('rubygems.quarantine','rubygems.release','rubygems.quarantine_read_policy.configure') THEN RAISE EXCEPTION 'owned quarantine audit failure'; END IF; RETURN NEW; END $$`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.ExecContext(t.Context(), `DROP TRIGGER IF EXISTS `+trigger+` ON resolver_audit_log`)
		_, _ = db.ExecContext(t.Context(), `DROP FUNCTION IF EXISTS `+trigger+`()`)
	}()
	if _, err = db.ExecContext(t.Context(), `CREATE TRIGGER `+trigger+` BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION `+trigger+`()`); err != nil {
		t.Fatal(err)
	}
	governanceChange(t, m.Preview(), repo.ID, p.Name, p.Version, p.Platform, p.Digest, "2", "release", "Bearer owned-admin", 500)
	stable, err := store.GetRetained(t.Context(), repo.ID, "isolated", "1.0.0", "ruby")
	if err != nil {
		t.Fatal(err)
	}
	// Clear the preparation quarantine, then prove a failed quarantine audit does
	// not introduce a restriction either.
	if stable.Governance.Quarantined {
		if _, err = db.ExecContext(t.Context(), `ALTER TABLE resolver_audit_log DISABLE TRIGGER `+trigger); err != nil {
			t.Fatal(err)
		}
		governanceChange(t, m.Preview(), repo.ID, stable.Name, stable.Version, stable.Platform, stable.Digest, stable.Governance.Version, "release", "Bearer owned-admin", 200)
		if _, err = db.ExecContext(t.Context(), `ALTER TABLE resolver_audit_log ENABLE TRIGGER `+trigger); err != nil {
			t.Fatal(err)
		}
		stable, err = store.GetRetained(t.Context(), repo.ID, stable.Name, stable.Version, stable.Platform)
		if err != nil {
			t.Fatal(err)
		}
	}
	governanceChange(t, m.Preview(), repo.ID, stable.Name, stable.Version, stable.Platform, stable.Digest, stable.Governance.Version, "quarantine", "Bearer owned-admin", 500)
	stableAfter, err := store.GetRetained(t.Context(), repo.ID, stable.Name, stable.Version, stable.Platform)
	if err != nil || stableAfter.Governance != stable.Governance {
		t.Fatal("audit quarantine rollback", stableAfter, err)
	}
	setReadPolicy(t, m, repo.ID, "3", true, 500)
	policy, err := store.GetReadPolicy(t.Context(), repo.ID)
	if err != nil || policy.Enabled || policy.Version != "3" {
		t.Fatal("audit policy rollback", policy, err)
	}
	got, err := store.GetRetained(t.Context(), repo.ID, p.Name, p.Version, p.Platform)
	if err != nil || !got.Governance.Quarantined || got.Governance.Version != "2" {
		t.Fatal("audit release rollback", got, err)
	}
}
