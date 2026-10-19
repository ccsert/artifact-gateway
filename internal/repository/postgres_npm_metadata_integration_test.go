//go:build integration

package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestPostgresNPMMetadataContract(t *testing.T) {
	store, second, repo := openPostgresNPMDistTagStores(t)
	exerciseNPMMetadataContract(t, store, repo.ID)
	pkg, err := second.GetNPMPackage(context.Background(), repo.ID, "@scope/governed")
	if err != nil || len(pkg.Versions) != 0 || len(pkg.TombstonedVersions) != 2 {
		t.Fatalf("other instance lost committed tombstones: %#v %v", pkg, err)
	}
	var audits int
	if err = second.db.QueryRow(`SELECT count(*) FROM resolver_audit_log WHERE repository=$1 AND operation IN ('npm.deprecate','npm.unpublish')`, repo.ID).Scan(&audits); err != nil || audits != 5 {
		t.Fatalf("committed governance audits=%d %v", audits, err)
	}
}

func TestPostgresNPMMetadataConcurrentCAS(t *testing.T) {
	first, second, repo := openPostgresNPMDistTagStores(t)
	ctx := context.Background()
	const name = "race-metadata"
	publishNPMDistTagFixture(t, first, repo.ID, name, "1.0.0", map[string]string{"latest": "1.0.0"})
	pkg, err := first.GetNPMPackage(ctx, repo.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for index, store := range []*PostgresStore{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			m := NPMMetadataMutation{RepositoryID: repo.ID, PackageName: name, ExpectedRevision: pkg.Revision, Audit: npmDistTagMutation(repo.ID, name, "", "").Audit}
			m.Audit.Operation = "npm.deprecate"
			if index == 0 {
				m.Deprecations = map[string]string{"1.0.0": "upgrade"}
			} else {
				m.Unpublish = []string{"1.0.0"}
				m.DistTags = map[string]string{}
				m.Audit.Operation = "npm.unpublish"
			}
			_, e := store.UpdateNPMMetadata(ctx, m)
			results <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrVersionConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS success=%d conflict=%d", success, conflict)
	}
}

func TestPostgresNPMMetadataPublishRace(t *testing.T) {
	first, second, repo := openPostgresNPMDistTagStores(t)
	exerciseNPMMetadataPublishRace(t, first, second, repo.ID)
}

func TestPostgresNPMMetadataAuditFailureRollsBack(t *testing.T) {
	store, _, repo := openPostgresNPMDistTagStores(t)
	ctx := context.Background()
	const name = "audit-rollback"
	publishNPMDistTagFixture(t, store, repo.ID, name, "1.0.0", map[string]string{"latest": "1.0.0"})
	pkg, err := store.GetNPMPackage(ctx, repo.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	// The trigger fails only this fixture's actor, leaving unrelated audits
	// and concurrent tests in the owned database untouched.
	_, err = store.db.Exec(`CREATE FUNCTION npm_governance_reject_fixture_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.actor='npm-governance-reject-audit' THEN RAISE EXCEPTION 'fixture audit unavailable'; END IF; RETURN NEW; END $$;
	 CREATE TRIGGER npm_governance_reject_fixture_audit BEFORE INSERT ON resolver_audit_log FOR EACH ROW EXECUTE FUNCTION npm_governance_reject_fixture_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.Exec(`DROP TRIGGER IF EXISTS npm_governance_reject_fixture_audit ON resolver_audit_log; DROP FUNCTION IF EXISTS npm_governance_reject_fixture_audit()`)
	})
	for _, unpublish := range []bool{false, true} {
		m := NPMMetadataMutation{RepositoryID: repo.ID, PackageName: name, ExpectedRevision: pkg.Revision, Deprecations: map[string]string{"1.0.0": "upgrade"}, Audit: npmDistTagMutation(repo.ID, name, "", "").Audit}
		m.Audit.Actor = "npm-governance-reject-audit"
		if unpublish {
			m.Deprecations = nil
			m.Unpublish = []string{"1.0.0"}
			m.DistTags = map[string]string{}
		}
		if _, err = store.UpdateNPMMetadata(ctx, m); err == nil {
			t.Fatal("failed audit committed metadata")
		}
		current, e := store.GetNPMPackage(ctx, repo.ID, name)
		if e != nil || current.Revision != pkg.Revision || len(current.Versions) != 1 || current.Versions[0].Deprecated != "" || current.DistTags["latest"] != "1.0.0" || len(current.TombstonedVersions) != 0 {
			t.Fatalf("partial transaction leaked: %#v %v", current, e)
		}
	}
	if _, err = store.TombstoneNPMVersion(ctx, repo.ID, name, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.GetNPMPackage(ctx, repo.ID, name)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RestoreNPMVersion(ctx, NPMRestoreRequest{RepositoryID: repo.ID, PackageName: name, Version: "1.0.0", Audit: AuditRecord{Actor: "npm-governance-reject-audit"}})
	if err == nil {
		t.Fatal("failed restore audit committed visibility")
	}
	current, err := store.GetNPMPackage(ctx, repo.ID, name)
	if err != nil || current.Revision != deleted.Revision || len(current.Versions) != 0 || len(current.TombstonedVersions) != 1 || len(current.DistTags) != 0 {
		t.Fatalf("restore audit failure leaked visibility: %#v %v", current, err)
	}
}
