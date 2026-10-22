//go:build integration

package repository

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresNuGetGovernanceStoreContract(t *testing.T) {
	testNuGetGovernanceStoreContract(t, newPostgresNuGetStore(t))
}

func TestPostgresNuGetGovernanceAuditRollbackAndReopen(t *testing.T) {
	store := newPostgresNuGetStore(t)
	repo, p := seedNuGetGovernance(t, store)
	name := "nuget_governance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	query := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.repository='%s' AND NEW.actor='nuget-governance-audit-failure' THEN RAISE EXCEPTION 'owned NuGet audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER %s BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION %s()`, name, repo.Name, name, name)
	if _, err := store.db.ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := newPostgresNuGetStore(t)
		if _, err := cleanup.db.Exec(`DROP TRIGGER ` + name + ` ON resolver_audit_log; DROP FUNCTION ` + name + `()`); err != nil {
			t.Error(err)
		}
	})
	in := NuGetGovernanceChange{RepositoryID: repo.ID, PackageID: p.PackageID, Version: p.Version, ExpectedDigest: p.Digest, ExpectedVersion: "1", Action: "delete", Actor: "nuget-governance-audit-failure", Reason: "owned rollback fixture"}
	if _, err := store.TransitionNuGetGovernance(t.Context(), in); err == nil {
		t.Fatal("failed audit committed governance")
	}
	if _, err := store.ReplaceNuGetQuarantineReadPolicy(t.Context(), repo.ID, RepositoryQuarantineReadPolicy{Enabled: true}, "1", in.Actor); err == nil {
		t.Fatal("failed audit committed read policy")
	}
	p, err := store.GetNuGetPublication(t.Context(), repo.ID, p.PackageID, p.Version)
	if err != nil || p.Governance.Version != "1" || p.Governance.Tombstoned {
		t.Fatalf("rollback=%+v %v", p.Governance, err)
	}
	policy, err := store.GetRepositoryQuarantineReadPolicy(t.Context(), repo.ID)
	if err != nil || policy.Enabled || policy.Version != "1" {
		t.Fatalf("policy rollback=%+v %v", policy, err)
	}
	in.Actor = "operator"
	if _, err = store.TransitionNuGetGovernance(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceNuGetQuarantineReadPolicy(t.Context(), repo.ID, RepositoryQuarantineReadPolicy{Enabled: true}, "1", in.Actor); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	store = newPostgresNuGetStore(t)
	p, err = store.GetNuGetPublication(t.Context(), repo.ID, p.PackageID, p.Version)
	if err != nil || p.Governance.Version != "2" || !p.Governance.Tombstoned {
		t.Fatalf("reopen=%+v %v", p.Governance, err)
	}
	policy, err = store.GetRepositoryQuarantineReadPolicy(t.Context(), repo.ID)
	if err != nil || !policy.Enabled || policy.Version != "2" {
		t.Fatalf("reopen policy=%+v %v", policy, err)
	}
}
