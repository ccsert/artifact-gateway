package rubygems

import (
	"context"
	"errors"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/repository"
	domain "github.com/ccsert/artifact-gateway/internal/rubygemspublication"
)

type readPolicyAudit struct {
	delegate domain.Auditor
	records  []repository.AuditRecord
	fail     bool
}

func (a *readPolicyAudit) RecordAudit(ctx context.Context, record repository.AuditRecord) error {
	a.records = append(a.records, record)
	if a.delegate != nil {
		if err := a.delegate.RecordAudit(ctx, record); err != nil {
			return err
		}
	}
	// Fail after delegation to exercise rollback of the repository's staged audit.
	if a.fail {
		return errors.New("owned read policy audit failure")
	}
	return nil
}

func TestRubyGemsReadPolicyUsesInjectedAuditAtomically(t *testing.T) {
	for _, mode := range []string{"separate", "delegating", "default"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			audit := &readPolicyAudit{}
			var sink domain.Auditor = audit
			if mode == "delegating" {
				audit.delegate = f.repos
			}
			if mode == "default" {
				sink = f.repos
			}
			store := domain.NewMemory(f.repos, sink)
			m, err := New(options(f.repos, store, f.objects, f.repos))
			if err != nil {
				t.Fatal(err)
			}
			assertPolicy := func(version string, enabled bool, count int) {
				t.Helper()
				got, err := store.GetReadPolicy(t.Context(), f.repo.ID)
				if err != nil || got.Version != version || got.Enabled != enabled {
					t.Fatalf("policy = %+v, %v", got, err)
				}
				rows, err := f.repos.ListAudits(t.Context(), repository.AuditQuery{Operation: "rubygems.quarantine_read_policy.configure"})
				want := count
				if mode == "separate" {
					want = 0
				}
				if err != nil || len(rows) != want {
					t.Fatalf("repository audits = %+v, %v; want %d", rows, err, want)
				}
			}
			if mode != "default" {
				audit.fail = true
				setReadPolicy(t, m, f.repo.ID, "1", true, 500)
				assertPolicy("1", false, 0)
				if len(audit.records) != 1 {
					t.Fatalf("failed audit calls = %d", len(audit.records))
				}
				audit.records = nil
				audit.fail = false
			}
			setReadPolicy(t, m, f.repo.ID, "1", true, 200)
			assertPolicy("2", true, 1)
			if mode != "default" {
				if len(audit.records) != 1 {
					t.Fatalf("successful audit calls = %d", len(audit.records))
				}
				record := audit.records[0]
				if record.Operation != "rubygems.quarantine_read_policy.configure" || record.Repository != f.repo.Name || record.Resource != f.repo.ID || record.Evidence["version"] != "2" || record.Evidence["enabled"] != "true" {
					t.Fatalf("audit = %+v", record)
				}
				audit.fail = true
			}
			setReadPolicy(t, m, f.repo.ID, "2", true, 200)
			setReadPolicy(t, m, f.repo.ID, "1", false, 409)
			assertPolicy("2", true, 1)
			if mode != "default" && len(audit.records) != 1 {
				t.Fatalf("noop or conflict audited: %+v", audit.records)
			}
		})
	}
}
