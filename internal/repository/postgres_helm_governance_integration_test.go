//go:build integration

package repository

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresHelmGovernanceStoreContract(t *testing.T) {
	testHelmGovernanceStoreContract(t, newPostgresNuGetStore(t))
}

func TestPostgresHelmGovernanceAuditRollbackAndReopen(t *testing.T) {
	store := newPostgresNuGetStore(t)
	repo, p := seedHelmGovernance(t, store, RepositoryTypeProxy)
	name := "helm_governance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	query := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.repository='%s' AND NEW.actor='helm-governance-audit-failure' THEN RAISE EXCEPTION 'owned Helm audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER %s BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION %s()`, name, repo.Name, name, name)
	if _, err := store.db.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := newPostgresNuGetStore(t)
		if _, err := cleanup.db.Exec(`DROP TRIGGER ` + name + ` ON resolver_audit_log; DROP FUNCTION ` + name + `()`); err != nil {
			t.Error(err)
		}
	})
	in := HelmGovernanceChange{RepositoryID: repo.ID, Name: p.Name, Version: p.Version, ExpectedDigest: p.Digest, ExpectedVersion: "1", Action: "delete", Actor: "helm-governance-audit-failure", Reason: "owned rollback fixture"}
	if _, err := store.TransitionHelmGovernance(t.Context(), in); err == nil {
		t.Fatal("failed audit committed governance")
	}
	if _, err := store.ReplaceHelmQuarantineReadPolicy(t.Context(), repo.ID, RepositoryQuarantineReadPolicy{Enabled: true}, "1", in.Actor); err == nil {
		t.Fatal("failed audit committed read policy")
	}
	p, err := store.GetHelmPublication(t.Context(), repo.ID, p.Name, p.Version)
	if err != nil || p.Governance.Version != "1" || p.Governance.Tombstoned {
		t.Fatalf("rollback=%+v %v", p.Governance, err)
	}
	policy, err := store.GetRepositoryQuarantineReadPolicy(t.Context(), repo.ID)
	if err != nil || policy.Enabled || policy.Version != "1" {
		t.Fatalf("policy rollback=%+v %v", policy, err)
	}
	in.Actor = "operator"
	if _, err = store.TransitionHelmGovernance(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceHelmQuarantineReadPolicy(t.Context(), repo.ID, RepositoryQuarantineReadPolicy{Enabled: true}, "1", in.Actor); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store = newPostgresNuGetStore(t)
	p, err = store.GetHelmPublication(t.Context(), repo.ID, p.Name, p.Version)
	if err != nil || p.Governance.Version != "2" || !p.Governance.Tombstoned {
		t.Fatalf("reopen=%+v %v", p.Governance, err)
	}
	policy, err = store.GetRepositoryQuarantineReadPolicy(t.Context(), repo.ID)
	if err != nil || !policy.Enabled || policy.Version != "2" {
		t.Fatalf("reopen policy=%+v %v", policy, err)
	}
}
