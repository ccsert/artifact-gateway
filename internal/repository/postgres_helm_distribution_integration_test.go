//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	dist "github.com/ccsert/artifact-gateway/internal/helmdistribution"
	"github.com/google/uuid"
)

func TestPostgresHelmReplicationStoreContract(t *testing.T) {
	testHelmReplicationStoreContract(t, newPostgresNuGetStore(t))
}
func TestPostgresHelmReplicationAuditRollbackAndReopen(t *testing.T) {
	s := newPostgresNuGetStore(t)
	f := helmReplicationContractSetup(t, s)
	j, _, err := s.CreateHelmReplication(t.Context(), f.job, f.check)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimHelmReplication(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	session := helmReplicationContractStage(t, s, f, claim)
	name := "helm_repl_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = s.db.ExecContext(t.Context(), `CREATE FUNCTION `+name+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation='helm.replication.item' AND NEW.actor='operator' AND NEW.repository='`+f.target.Name+`' THEN RAISE EXCEPTION 'owned Helm replication audit failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(t.Context(), `CREATE TRIGGER `+name+` BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION `+name+`() `); err != nil {
		t.Fatal(err)
	}
	drop := func() {
		_, _ = s.db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS `+name+` ON resolver_audit_log`)
		_, _ = s.db.ExecContext(context.Background(), `DROP FUNCTION IF EXISTS `+name+`()`)
	}
	t.Cleanup(drop)
	if _, err = s.CompleteHelmReplicationItem(t.Context(), claim.Lease, 0, dist.Commit{SessionID: session.ID}, f.check); err == nil {
		t.Fatal("audit failure did not reject publication")
	}
	if _, err = s.GetHelmPublication(t.Context(), f.target.ID, "acme", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("publication survived audit rollback=%v", err)
	}
	reopened := newPostgresNuGetStore(t)
	saved, err := reopened.GetHelmReplication(t.Context(), j.ID)
	if err != nil || saved.Items[0].State != "running" || saved.Items[0].TargetSessionID != session.ID {
		t.Fatalf("checkpoint=%+v %v", saved, err)
	}
	drop()
	if _, err = reopened.CompleteHelmReplicationItem(t.Context(), claim.Lease, 0, dist.Commit{SessionID: session.ID}, f.check); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.FinishHelmReplication(t.Context(), claim.Lease); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM resolver_audit_log WHERE repository=$1 AND operation='helm.replication.item'`, f.target.Name).Scan(&count); err != nil || count != 1 {
		t.Fatalf("transactional item audit=%d %v", count, err)
	}
}
func waitHelmReplicationLock(t *testing.T, s *PostgresStore, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var event sql.NullString
		if err := s.db.QueryRowContext(t.Context(), `SELECT wait_event_type FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&event); err != nil {
			t.Fatal(err)
		}
		if event.Valid && event.String == "Lock" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("concurrent authority mutation did not block on the real publication transaction lock")
}
func TestPostgresHelmReplicationAuthorityMutationSerializesWithPublication(t *testing.T) {
	for _, mode := range []string{"key-revoke", "user-demote", "service-disable", "credential-revoke", "grant-revoke"} {
		t.Run(mode, func(t *testing.T) {
			s := newPostgresNuGetStore(t)
			f := helmReplicationContractSetup(t, s)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			mutator := newPostgresNuGetStore(t)
			mutator.db.SetMaxOpenConns(1)
			mutator.db.SetMaxIdleConns(1)
			var pid int
			if err := mutator.db.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			actor := ""
			kind := ""
			credential := ""
			mutate := func() error { return nil }
			switch mode {
			case "key-revoke":
				key, err := s.CreateAPIKey(ctx, APIKey{ID: uuid.NewString(), Name: "owned-replication-key", SecretHash: uuid.NewString(), Roles: []string{"admin"}})
				if err != nil {
					t.Fatal(err)
				}
				actor = "api-key:" + key.ID
				kind = "api_key"
				mutate = func() error { _, err := mutator.RevokeAPIKey(ctx, key.ID); return err }
			case "user-demote":
				if _, err := s.CreateUser(ctx, User{ID: uuid.NewString(), Name: "standby-" + uuid.NewString(), Role: "admin"}); err != nil {
					t.Fatal(err)
				}
				u, err := s.CreateUser(ctx, User{ID: uuid.NewString(), Name: "repl-" + uuid.NewString(), Role: "admin"})
				if err != nil {
					t.Fatal(err)
				}
				actor = "user:" + u.Name
				kind = "local_session"
				role := "member"
				mutate = func() error {
					_, err := mutator.UpdateUser(ctx, UserUpdate{ID: u.ID, Role: &role}, u.Version)
					return err
				}
			case "service-disable", "credential-revoke":
				account, err := s.CreateServiceAccount(ctx, ServiceAccount{ID: uuid.NewString(), Name: "repl-" + uuid.NewString()})
				if err != nil {
					t.Fatal(err)
				}
				key, err := s.CreateServiceAccountCredential(ctx, APIKey{ID: uuid.NewString(), ServiceAccountID: account.ID, Name: "owned-credential", SecretHash: uuid.NewString(), Roles: []string{}})
				if err != nil {
					t.Fatal(err)
				}
				actor = "service-account:" + account.ID
				kind = "service_account_credential"
				credential = key.ID
				if mode == "service-disable" {
					state := ServiceAccountDisabled
					mutate = func() error {
						_, err := mutator.UpdateServiceAccount(ctx, ServiceAccountUpdate{ID: account.ID, State: &state}, account.Version)
						return err
					}
				} else {
					mutate = func() error { _, err := mutator.RevokeServiceAccountCredential(ctx, account.ID, key.ID); return err }
				}
			case "grant-revoke":
				actor = "operator"
				kind = "static_admin"
			}
			if _, err := s.ReplaceRepositoryGrants(ctx, f.target.ID, []RepositoryGrant{{Principal: actor, Scopes: []string{"repositories:write"}}}, "1"); err != nil {
				t.Fatal(err)
			}
			if mode == "grant-revoke" {
				mutate = func() error {
					_, err := mutator.ReplaceRepositoryGrants(ctx, f.target.ID, []RepositoryGrant{}, "2")
					return err
				}
			}
			plan := dist.Plan{SourceRepositoryID: f.source.ID, TargetRepositoryID: f.target.ID, Items: []dist.Identity{f.job.Items[0].Identity}}
			job, err := dist.NewJob(plan, actor, kind, credential, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			f.job = job
			permitted := func(_ context.Context, a dist.Authority) error {
				if !a.Active || mode == "user-demote" && a.Role != "admin" || len(a.Target.Grants) != 1 {
					return dist.ErrAccessDenied
				}
				return nil
			}
			f.check = permitted
			if _, _, err = s.CreateHelmReplication(ctx, job, permitted); err != nil {
				t.Fatal(err)
			}
			claim, err := s.ClaimHelmReplication(ctx, uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			session := helmReplicationContractStage(t, s, f, claim)
			inside := make(chan struct{})
			release := make(chan struct{})
			done := make(chan error, 1)
			mutationDone := make(chan error, 1)
			finalCheck := func(ctx context.Context, a dist.Authority) error {
				close(inside)
				select {
				case <-release:
				case <-ctx.Done():
					return ctx.Err()
				}
				return permitted(ctx, a)
			}
			go func() {
				_, err := s.CompleteHelmReplicationItem(ctx, claim.Lease, 0, dist.Commit{SessionID: session.ID}, finalCheck)
				done <- err
			}()
			select {
			case <-inside:
			case err := <-done:
				t.Fatalf("did not enter final transaction=%v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			go func() { mutationDone <- mutate() }()
			waitHelmReplicationLock(t, s, pid)
			close(release)
			if err = <-done; err != nil {
				t.Fatal(err)
			}
			if err = <-mutationDone; err != nil {
				t.Fatal(err)
			}
			if _, err = s.FinishHelmReplication(ctx, claim.Lease); err != nil {
				t.Fatal(err)
			}
			// The mutation completed after publication. Fresh work sees the revoked
			// state, rather than restoring the original administrator/credential claim.
			replacement, err := dist.NewJob(plan, actor, kind, credential, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = s.CreateHelmReplication(ctx, replacement, permitted); !errors.Is(err, dist.ErrAccessDenied) {
				t.Fatalf("completed mutation bypassed=%v", err)
			}
		})
	}
}
func TestPostgresHelmReplicationCredentialExpiryDuringFinalTransactionRollsBack(t *testing.T) {
	s := newPostgresNuGetStore(t)
	f := helmReplicationContractSetup(t, s)
	expiry := time.Now().Add(time.Second)
	key, err := s.CreateAPIKey(t.Context(), APIKey{ID: uuid.NewString(), Name: "expiry-boundary", SecretHash: uuid.NewString(), Roles: []string{"admin"}, ExpiresAt: &expiry})
	if err != nil {
		t.Fatal(err)
	}
	plan := dist.Plan{SourceRepositoryID: f.source.ID, TargetRepositoryID: f.target.ID, Items: []dist.Identity{f.job.Items[0].Identity}}
	job, err := dist.NewJob(plan, "api-key:"+key.ID, "api_key", "", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	f.job = job
	f.check = func(_ context.Context, a dist.Authority) error {
		if !a.Active {
			return dist.ErrAccessDenied
		}
		return nil
	}
	if _, _, err = s.CreateHelmReplication(t.Context(), job, f.check); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimHelmReplication(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	session := helmReplicationContractStage(t, s, f, claim)
	final := func(ctx context.Context, a dist.Authority) error {
		if err := f.check(ctx, a); err != nil {
			return err
		}
		timer := time.NewTimer(time.Until(expiry) + 20*time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if _, err = s.CompleteHelmReplicationItem(t.Context(), claim.Lease, 0, dist.Commit{SessionID: session.ID}, final); !errors.Is(err, dist.ErrAccessDenied) {
		t.Fatalf("expired credential committed=%v", err)
	}
	if _, err = s.GetHelmPublication(t.Context(), f.target.ID, "acme", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired publication visible=%v", err)
	}
	if err = s.FailHelmReplicationItem(t.Context(), claim.Lease, 0, "access_denied"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishHelmReplication(t.Context(), claim.Lease); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresHelmReplicationFinalGovernanceAndClosureQuota(t *testing.T) {
	for _, mode := range []string{"source-quarantine", "target-quota"} {
		t.Run(mode, func(t *testing.T) {
			s := newPostgresNuGetStore(t)
			f := helmReplicationContractSetup(t, s)
			if _, _, err := s.CreateHelmReplication(t.Context(), f.job, f.check); err != nil {
				t.Fatal(err)
			}
			claim, err := s.ClaimHelmReplication(t.Context(), uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			session := helmReplicationContractStage(t, s, f, claim)
			want := dist.ErrSourceChanged
			if mode == "source-quarantine" {
				_, err = s.TransitionHelmGovernance(t.Context(), HelmGovernanceChange{RepositoryID: f.source.ID, Name: f.publication.Name, Version: f.publication.Version, ExpectedDigest: f.publication.Digest, ExpectedVersion: f.publication.Governance.Version, Actor: "operator", Action: "quarantine", Reason: "changed after target reservation"})
			} else {
				_, err = s.ReplaceRepositoryCapacityQuota(t.Context(), f.target.ID, f.publication.Size)
				want = ErrQuotaExceeded
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.CompleteHelmReplicationItem(t.Context(), claim.Lease, 0, dist.Commit{SessionID: session.ID}, f.check); !errors.Is(err, want) {
				t.Fatalf("final %s=%v", mode, err)
			}
			if _, err = s.GetHelmPublication(t.Context(), f.target.ID, f.publication.Name, f.publication.Version); !errors.Is(err, ErrNotFound) {
				t.Fatalf("rejected closure visible=%v", err)
			}
			saved, err := s.GetHelmReplication(t.Context(), f.job.ID)
			if err != nil || saved.Items[0].State != "running" {
				t.Fatalf("failed commit changed checkpoint=%+v %v", saved, err)
			}
			if err = s.FailHelmReplicationItem(t.Context(), claim.Lease, 0, "transfer_failed"); err != nil {
				t.Fatal(err)
			}
			if _, err = s.FinishHelmReplication(t.Context(), claim.Lease); err != nil {
				t.Fatal(err)
			}
		})
	}
}
