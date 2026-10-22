//go:build integration

package pgstore_test

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/google/uuid"
)

func scheduleReplica(t *testing.T) (*pgstore.Store, string) {
	t.Helper()
	connection, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	name := "schedule-control-" + uuid.NewString()
	query := connection.Query()
	query.Set("application_name", name)
	connection.RawQuery = query.Encode()
	db, err := sql.Open("pgx", connection.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return pgstore.New(db), name
}

func scheduleReplicaBlocked(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var blocked bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, name).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	return blocked
}

func waitScheduleReplicaBlocked(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if scheduleReplicaBlocked(t, db, name) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("replica did not reach the owned database lock")
}

func TestPostgresMigrationScheduleControlRejectsOldBatchReactivation(t *testing.T) {
	for _, kind := range []string{"manual-template", "old-scheduled-batch"} {
		t.Run(kind, func(t *testing.T) {
			f := newScheduleFixture(t)
			p := f.create(true)
			f.run()
			p = f.get(p.ID)
			old := f.template
			if kind == "old-scheduled-batch" {
				if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET state='completed',scan_complete=true WHERE id=$1`, p.ActiveBatchID); err != nil {
					t.Fatal(err)
				}
				var err error
				old, err = f.ledger.Get(t.Context(), p.ActiveBatchID)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.ledger.ReconcileSchedules(t.Context(), 100); err != nil {
					t.Fatal(err)
				}
				p = f.get(p.ID)
				e := f.evidence(time.Now().Add(-time.Millisecond))
				if _, err = f.plans.Control(t.Context(), p.ID, p.Version, f.actor, uuid.NewString(), "capacity", &e); err != nil {
					t.Fatal(err)
				}
				f.due(p.ID)
				f.run()
				p = f.get(p.ID)
			}
			e := f.evidence(time.Now().Add(-time.Millisecond))
			_, err := f.ledger.Control(t.Context(), old.ID, old.Version, f.actor, uuid.NewString(), "retry", uuid.NewString(), &e)
			if err == nil || err.Error() != "mapping_active_batch" {
				t.Fatalf("old %s bypassed active batch %s: %v", kind, p.ActiveBatchID, err)
			}
			current, err := f.ledger.Get(t.Context(), old.ID)
			if err != nil || current.State != "completed" || current.Version != old.Version {
				t.Fatalf("rejected activation changed batch: %+v %v", current, err)
			}
		})
	}
}

func TestPostgresMigrationScheduleCurrentBatchResumeExcludesItselfAndReplaysFirst(t *testing.T) {
	f := newScheduleFixture(t)
	p := f.create(true)
	f.run()
	p = f.get(p.ID)
	b, err := f.ledger.Get(t.Context(), p.ActiveBatchID)
	if err != nil {
		t.Fatal(err)
	}
	b, err = f.ledger.Control(t.Context(), b.ID, b.Version, f.actor, "pause-current", "pause", "pause-hash", nil)
	if err != nil || b.State != "paused" {
		t.Fatalf("pause current: %+v %v", b, err)
	}
	e := f.evidence(time.Now().Add(-time.Millisecond))
	resumed, err := f.ledger.Control(t.Context(), b.ID, b.Version, f.actor, "resume-current", "resume", "resume-hash", &e)
	if err != nil || resumed.State != "scanning" || resumed.Version == b.Version {
		t.Fatalf("current batch blocked itself: %+v %v", resumed, err)
	}
	if _, err = f.db.Exec(`UPDATE nexus_migration_batches SET state='completed',scan_complete=true WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.ledger.ReconcileSchedules(t.Context(), 100); err != nil {
		t.Fatal(err)
	}
	p = f.get(p.ID)
	if _, err = f.plans.Control(t.Context(), p.ID, p.Version, f.actor, uuid.NewString(), "capacity", &e); err != nil {
		t.Fatal(err)
	}
	f.due(p.ID)
	f.run()
	replayed, err := f.ledger.Control(t.Context(), b.ID, b.Version, f.actor, "resume-current", "resume", "resume-hash", &e)
	if err != nil || replayed.Version != resumed.Version || replayed.State != resumed.State {
		t.Fatalf("replay reevaluated later overlap/CAS: %+v %v", replayed, err)
	}
	if _, err = f.ledger.Control(t.Context(), b.ID, b.Version, f.actor, "resume-current", "resume", "different-hash", &e); !errors.Is(err, m.ErrIdempotencyConflict) {
		t.Fatalf("changed replay hash: %v", err)
	}
	if _, err = f.ledger.Control(t.Context(), b.ID, "stale", f.actor, uuid.NewString(), "retry", uuid.NewString(), &e); !errors.Is(err, m.ErrVersionConflict) {
		t.Fatalf("CAS fence: %v", err)
	}
}

func TestPostgresMigrationScheduleControlAndDispatchSerializeActivation(t *testing.T) {
	f := newScheduleFixture(t)
	p := f.create(true)
	f.due(p.ID)
	claim, err := f.ledger.ClaimDueSchedule(t.Context(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err = blocker.Exec(`SELECT id FROM nexus_migration_batches WHERE id=$1 FOR UPDATE`, f.template.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	control, controlName := scheduleReplica(t)
	dispatch, dispatchName := scheduleReplica(t)
	controlled := make(chan error, 1)
	dispatched := make(chan error, 1)
	e := f.evidence(time.Now().Add(-time.Millisecond))
	go func() {
		_, controlErr := control.Control(ctx, f.template.ID, f.template.Version, f.actor, uuid.NewString(), "retry", uuid.NewString(), &e)
		controlled <- controlErr
	}()
	waitScheduleReplicaBlocked(t, f.db, controlName)
	go func() { dispatched <- dispatch.DispatchSchedule(ctx, claim, dispatchBatch(claim), time.Now()) }()
	var dispatchErr error
	finished := false
	deadline := time.Now().Add(5 * time.Second)
	for !finished && !scheduleReplicaBlocked(t, f.db, dispatchName) && time.Now().Before(deadline) {
		select {
		case dispatchErr = <-dispatched:
			finished = true
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err = blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	controlErr := <-controlled
	if !finished {
		dispatchErr = <-dispatched
	}
	var active int
	if err = f.db.QueryRow(`SELECT count(*) FROM nexus_migration_batches WHERE source_id=$1 AND state NOT IN ('completed','failed','cancelled')`, f.template.SourceID).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 || (controlErr == nil) == (dispatchErr == nil) {
		t.Fatalf("concurrent activation: active=%d control=%v dispatch=%v", active, controlErr, dispatchErr)
	}
	for _, failure := range []error{controlErr, dispatchErr} {
		if failure != nil && failure.Error() != "mapping_active_batch" {
			t.Fatalf("activation failed outside the target fence: %v", failure)
		}
	}
}

func TestPostgresMigrationScheduleReactivationTakesDispatchBeforeBatchRow(t *testing.T) {
	f := newScheduleFixture(t)
	blocker, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback() }()
	if _, err = blocker.Exec(`SELECT pg_advisory_xact_lock(hashtextextended('nexus-migration-dispatch',0))`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	replica, name := scheduleReplica(t)
	done := make(chan error, 1)
	e := f.evidence(time.Now().Add(-time.Millisecond))
	go func() {
		_, controlErr := replica.Control(ctx, f.template.ID, f.template.Version, f.actor, uuid.NewString(), "retry", uuid.NewString(), &e)
		done <- controlErr
	}()
	waitScheduleReplicaBlocked(t, f.db, name)
	// A schedule creator holds dispatch before reading its template. The
	// waiting control must not already own that batch row or publication lease.
	var leaseAvailable bool
	if err = blocker.QueryRow(`SELECT pg_try_advisory_xact_lock(hashtextextended('nexus-migration-lease:'||$1,0))`, f.template.ID).Scan(&leaseAvailable); err != nil || !leaseAvailable {
		t.Fatalf("reactivation inverted publication lock order: available=%t %v", leaseAvailable, err)
	}
	if _, err = blocker.Exec(`SELECT id FROM nexus_migration_batches WHERE id=$1 FOR UPDATE NOWAIT`, f.template.ID); err != nil {
		t.Fatalf("reactivation inverted batch row lock order: %v", err)
	}
	if err = blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatalf("reactivation after dispatch release: %v", err)
	}
}

func TestPostgresMigrationScheduleDispatchLeaseExpiresDuringBindingLockWait(t *testing.T) {
	for _, binding := range []string{"source", "target"} {
		t.Run(binding, func(t *testing.T) {
			f := newScheduleFixture(t)
			p := f.create(true)
			f.due(p.ID)
			claim, err := f.ledger.ClaimDueSchedule(t.Context(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.db.Exec(`UPDATE nexus_migration_schedules SET lease_expires_at=clock_timestamp()+interval '1 second' WHERE id=$1`, p.ID); err != nil {
				t.Fatal(err)
			}
			blocker, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback() }()
			query, id := `SELECT id FROM hosted_repositories WHERE id=$1 FOR UPDATE`, p.Mappings[0].TargetRepositoryID
			if binding == "source" {
				query, id = `SELECT id FROM nexus_migration_sources WHERE id=$1 FOR UPDATE`, p.SourceID
			}
			if _, err = blocker.Exec(query, id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			replica, name := scheduleReplica(t)
			done := make(chan error, 1)
			go func() { done <- replica.DispatchSchedule(ctx, claim, dispatchBatch(claim), time.Now()) }()
			waitScheduleReplicaBlocked(t, f.db, name)
			for {
				var expired bool
				if err = f.db.QueryRow(`SELECT lease_expires_at<=clock_timestamp() FROM nexus_migration_schedules WHERE id=$1`, p.ID).Scan(&expired); err != nil {
					t.Fatal(err)
				}
				if expired {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err = blocker.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err = <-done; !errors.Is(err, m.ErrLeaseLost) {
				t.Fatalf("expired dispatcher crossed %s lock wait: %v", binding, err)
			}
			current := f.get(p.ID)
			if f.count(p.ID) != 0 || current.ActiveBatchID != "" || current.LastBatchID != claim.LastBatchID || current.State != claim.State || current.CapacityEvidence == nil || current.ConsumedObservedAt == nil || !current.ConsumedObservedAt.Equal(*claim.ConsumedObservedAt) || !current.CapacityEvidence.ObservedAt.Equal(claim.CapacityEvidence.ObservedAt) || !current.CapacityEvidence.ValidUntil.Equal(claim.CapacityEvidence.ValidUntil) || current.Version != claim.Version || current.LeaseToken != claim.LeaseToken || current.LeaseEpoch != claim.LeaseEpoch || !current.NextRunAt.Equal(*claim.NextRunAt) {
				t.Fatalf("expired dispatch changed durable state/evidence: %+v", current)
			}
			var sourceVersion, targetState string
			if err = f.db.QueryRow(`SELECT version::text FROM nexus_migration_sources WHERE id=$1`, p.SourceID).Scan(&sourceVersion); err != nil || sourceVersion != claim.SourceVersion {
				t.Fatalf("test changed source version: %s %v", sourceVersion, err)
			}
			if err = f.db.QueryRow(`SELECT state FROM hosted_repositories WHERE id=$1`, p.Mappings[0].TargetRepositoryID).Scan(&targetState); err != nil || targetState != "active" {
				t.Fatalf("test changed target state: %s %v", targetState, err)
			}
		})
	}
}
