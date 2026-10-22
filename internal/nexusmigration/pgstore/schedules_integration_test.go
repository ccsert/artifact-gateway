//go:build integration

package pgstore_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ccsert/artifact-gateway/internal/app"
	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/nexusmigration/pgstore"
	"github.com/ccsert/artifact-gateway/internal/nexussource"
	"github.com/ccsert/artifact-gateway/internal/objectstore"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/ccsert/artifact-gateway/internal/testsupport"
	"github.com/google/uuid"
)

type scheduleSource struct {
	m.Source
	db *sql.DB
}

func (s scheduleSource) Get(ctx context.Context, id string) (nexussource.Source, error) {
	var source nexussource.Source
	source.ID = id
	err := s.db.QueryRowContext(ctx, `SELECT version::text FROM nexus_migration_sources WHERE id::text=$1`, id).Scan(&source.Version)
	return source, err
}

type schedulePublication struct {
	m.Publication
	fail atomic.Bool
}

func (p *schedulePublication) ValidateTargets(context.Context, []m.Mapping) error {
	if p.fail.Load() {
		return errors.New("untrusted infrastructure diagnostic must not enter plan state")
	}
	return nil
}

type scheduleFixture struct {
	t           *testing.T
	db          *sql.DB
	ledger      *pgstore.Store
	batches     *m.Service
	plans       *m.ScheduleService
	publication *schedulePublication
	template    m.Batch
	actor       string
}

// These PostgreSQL-only fixtures verify schedule/lease transactions. They do
// not claim artifact-copy evidence; the real byte lane below uses newFixture.
func newScheduleFixture(t *testing.T) *scheduleFixture {
	t.Helper()
	connection := os.Getenv("TEST_DATABASE_URL")
	if connection == "" {
		t.Skip("isolated PostgreSQL required")
	}
	if err := testsupport.ValidateIsolatedDatabaseURL(connection); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	sourceID, repositoryID := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(`INSERT INTO nexus_migration_sources(id,name,base_url) VALUES($1,'schedule-fixture','https://example.com')`, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO hosted_repositories(id,name,format) VALUES($1,$2,'raw')`, repositoryID, "schedule-"+strings.ReplaceAll(repositoryID, "-", "")[:20]); err != nil {
		t.Fatal(err)
	}
	ledger := pgstore.New(db)
	publication := &schedulePublication{}
	batches := m.New(ledger, scheduleSource{db: db}, publication, m.Options{TargetID: "schedule-target", PoolID: "schedule-pool"})
	f := &scheduleFixture{t: t, db: db, ledger: ledger, batches: batches, publication: publication, actor: "schedule-admin:" + sourceID}
	f.plans = m.NewSchedules(ledger, batches)
	in := m.StartInput{SourceID: sourceID, SourceVersion: "1", Mappings: []m.Mapping{{SourceRepository: "source", TargetRepositoryID: repositoryID}}, PolicyVersion: m.PolicyVersion, CapacityEvidence: f.evidence(time.Now().Add(-time.Minute))}
	f.template, err = batches.Start(context.Background(), in, f.actor, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE nexus_migration_batches SET state='completed',scan_complete=true,updated_at=clock_timestamp()-interval '10 seconds' WHERE id=$1`, f.template.ID); err != nil {
		t.Fatal(err)
	}
	f.template, err = batches.Get(context.Background(), f.template.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, e := db.Exec(`UPDATE nexus_migration_schedules SET state='stopped',next_run_at=NULL,lease_token='',lease_expires_at=NULL WHERE request->>'sourceId'=$1`, sourceID)
		if e != nil {
			t.Error(e)
		}
		_, e = db.Exec(`UPDATE nexus_migration_batches SET state='cancelled',lease_token='',lease_expires_at=NULL WHERE source_id=$1`, sourceID)
		if e != nil {
			t.Error(e)
		}
	})
	return f
}

func (f *scheduleFixture) evidence(observed time.Time) m.CapacityEvidence {
	zero, free, download := int64(0), int64(1<<30), int64(1<<20)
	return m.CapacityEvidence{TargetID: "schedule-target", PoolID: "schedule-pool", ObservedAt: observed.UTC(), ValidUntil: observed.Add(20 * time.Minute).UTC(), FreeBytes: &free, DownloadBytes: &download, UploadBytes: &zero, BackupBytes: &zero, RestoreBytes: &zero, HeadroomBytes: &zero}
}
func (f *scheduleFixture) create(withEvidence bool) m.Schedule {
	f.t.Helper()
	in := m.ScheduleCreate{TemplateBatchID: f.template.ID, IntervalSeconds: 60}
	if withEvidence {
		e := f.evidence(time.Now().Add(-time.Second))
		in.CapacityEvidence = &e
	}
	p, err := f.plans.Create(context.Background(), in, f.actor, uuid.NewString())
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}
func (f *scheduleFixture) get(id string) m.Schedule {
	f.t.Helper()
	p, err := f.plans.Get(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}
func (f *scheduleFixture) run() {
	f.t.Helper()
	if err := f.plans.RunDue(context.Background(), 100); err != nil {
		f.t.Fatal(err)
	}
}
func (f *scheduleFixture) due(id string) {
	f.t.Helper()
	if _, err := f.db.Exec(`UPDATE nexus_migration_schedules SET next_run_at=clock_timestamp()-interval '2 days' WHERE id=$1`, id); err != nil {
		f.t.Fatal(err)
	}
}
func (f *scheduleFixture) count(id string) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM nexus_migration_batches WHERE schedule_id=$1`, id).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
func dispatchBatch(p m.Schedule) m.Batch {
	return m.Batch{ID: uuid.NewString(), Actor: p.Actor, StartInput: m.StartInput{SourceID: p.SourceID, SourceVersion: p.SourceVersion, Mappings: p.Mappings, PolicyVersion: p.PolicyVersion, CapacityEvidence: *p.CapacityEvidence}}
}

func TestPostgresMigrationScheduleConcurrentReplicasCoalesceAndConsumeEvidenceOnce(t *testing.T) {
	f := newScheduleFixture(t)
	p := f.create(true)
	f.due(p.ID)
	db2, err := sql.Open("pgx", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db2.Close() }()
	second := m.NewSchedules(pgstore.New(db2), f.batches)
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			service := f.plans
			if i%2 == 0 {
				service = second
			}
			errors <- service.RunDue(context.Background(), 100)
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	p = f.get(p.ID)
	if f.count(p.ID) != 1 || p.ActiveBatchID == "" || p.CapacityEvidence != nil || p.ConsumedObservedAt == nil || p.NextRunAt == nil || time.Until(*p.NextRunAt) < 55*time.Second {
		t.Fatalf("non-atomic/coalesced plan: %+v", p)
	}
	f.due(p.ID)
	f.run()
	if f.count(p.ID) != 1 {
		t.Fatal("live batch overlapped another occurrence")
	}
	if _, err = f.batches.Start(context.Background(), f.template.StartInput, f.actor, uuid.NewString()); err == nil || err.Error() != "mapping_active_batch" {
		t.Fatalf("manual overlap: %v", err)
	}
	if _, err = f.db.Exec(`UPDATE nexus_migration_batches SET state='completed',scan_complete=true,updated_at=clock_timestamp() WHERE id=$1`, p.ActiveBatchID); err != nil {
		t.Fatal(err)
	}
	f.run()
	p = f.get(p.ID)
	if p.State != "waiting_capacity" || p.LastError != "capacity_evidence_refresh_required" || p.ActiveBatchID != "" || p.LastSuccessfulAt == nil || !p.LastSuccessfulAt.After(*f.templateTime()) {
		t.Fatalf("evidence reused or success absent: %+v", p)
	}
	old := f.evidence(*p.ConsumedObservedAt)
	if _, err = f.plans.Control(context.Background(), p.ID, p.Version, f.actor, uuid.NewString(), "capacity", &old); err == nil || err.Error() != "capacity_evidence_refresh_required" {
		t.Fatalf("old observation reused: %v", err)
	}
	fresh := f.evidence(time.Now().Add(-time.Millisecond))
	if _, err = f.plans.Control(context.Background(), p.ID, p.Version, f.actor, uuid.NewString(), "capacity", &fresh); err != nil {
		t.Fatal(err)
	}
	f.run()
	if f.count(p.ID) != 2 {
		t.Fatal("new evidence did not authorize exactly one new occurrence")
	}
}

func TestPostgresMigrationScheduleRejectsPartiallyOverlappingTargetMappings(t *testing.T) {
	for _, kind := range []string{"wide-plan-subset-manual", "subset-plan-wide-manual", "wide-plan-existing-subset-manual", "wide-plan-subset-plan"} {
		t.Run(kind, func(t *testing.T) {
			f := newScheduleFixture(t)
			other := uuid.NewString()
			if _, err := f.db.Exec(`INSERT INTO hosted_repositories(id,name,format) VALUES($1,$2,'raw')`, other, "overlap-"+strings.ReplaceAll(other, "-", "")[:20]); err != nil {
				t.Fatal(err)
			}
			extra := m.Mapping{SourceRepository: "other", TargetRepositoryID: other}
			manual := f.template.StartInput
			if kind == "subset-plan-wide-manual" {
				manual.Mappings = append([]m.Mapping{extra}, manual.Mappings...)
			} else {
				wide := f.template.StartInput
				wide.Mappings = append([]m.Mapping{extra}, wide.Mappings...)
				raw, _ := json.Marshal(wide)
				if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET request=$2 WHERE id=$1`, f.template.ID, raw); err != nil {
					t.Fatal(err)
				}
			}
			p := f.create(true)
			if kind == "wide-plan-subset-plan" {
				template, err := f.batches.Start(context.Background(), manual, f.actor, uuid.NewString())
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.db.Exec(`UPDATE nexus_migration_batches SET state='completed',scan_complete=true WHERE id=$1`, template.ID); err != nil {
					t.Fatal(err)
				}
				_, err = f.plans.Create(context.Background(), m.ScheduleCreate{TemplateBatchID: template.ID, IntervalSeconds: 120}, f.actor, uuid.NewString())
				if err == nil || err.Error() != "mapping_schedule_exists" {
					t.Fatalf("partially overlapping plan accepted: %v", err)
				}
				return
			}
			if kind == "wide-plan-existing-subset-manual" {
				if _, err := f.batches.Start(context.Background(), manual, f.actor, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
				f.run()
				p = f.get(p.ID)
				if p.State != "blocked" || p.LastError != "mapping_active_batch" || f.count(p.ID) != 0 {
					t.Fatalf("partial overlap dispatched: %+v", p)
				}
				return
			}
			f.run()
			if _, err := f.batches.Start(context.Background(), manual, f.actor, uuid.NewString()); err == nil || err.Error() != "mapping_active_batch" {
				t.Fatalf("partial overlap accepted: %v", err)
			}
			if f.count(p.ID) != 1 {
				t.Fatal("partial overlap created another scheduled batch")
			}
		})
	}
}

func TestPostgresMigrationScheduleNeverDecodesCommittedOpaqueAssetPayload(t *testing.T) {
	f := newScheduleFixture(t)
	f.db.SetMaxOpenConns(1)
	// An opaque future private metadata field and non-decoder size sentinel make
	// accidental use of scanEntry/totals fail. Schedule success only needs state.
	assets, _ := json.Marshal([]map[string]any{{"sourcePath": "opaque.bin", "size": "private-decoder-sentinel", "privatePackument": strings.Repeat("opaque-package-metadata", 100000)}})
	insert := func(id string) {
		t.Helper()
		if _, err := f.db.Exec(`INSERT INTO nexus_migration_entries(id,batch_id,ordinal,source_repository,target_repository_id,format,identity,state,assets) VALUES($1,$2,0,'source',$3,'raw','opaque.bin','committed',$4)`, uuid.NewString(), id, f.template.Mappings[0].TargetRepositoryID, assets); err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET entry_count=1 WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	insert(f.template.ID)
	p := f.create(true)
	f.run()
	p = f.get(p.ID)
	insert(p.ActiveBatchID)
	if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET state='completed',scan_complete=true WHERE id=$1`, p.ActiveBatchID); err != nil {
		t.Fatal(err)
	}
	f.run()
	p = f.get(p.ID)
	if p.State != "active" || p.LastSuccessfulAt == nil || !p.LastSuccessfulAt.After(f.template.UpdatedAt) {
		t.Fatalf("opaque payload was decoded or false failure: %+v", p)
	}
}

func TestPostgresMigrationScheduleChecksMavenRegenerationBeyondOnePage(t *testing.T) {
	f := newScheduleFixture(t)
	f.db.SetMaxOpenConns(1)
	if _, err := f.db.Exec(`UPDATE hosted_repositories SET format='maven' WHERE id=$1`, f.template.Mappings[0].TargetRepositoryID); err != nil {
		t.Fatal(err)
	}
	insert := func(id string) {
		t.Helper()
		_, err := f.db.Exec(`INSERT INTO nexus_migration_entries(id,batch_id,ordinal,source_repository,target_repository_id,format,identity,state,assets,last_error) SELECT gen_random_uuid(),$1,n,'source',$2,'maven','/unsupported/org/example/widget'||n||'/maven-metadata.xml','unsupported','[]'::jsonb,'maven_mixed_metadata_requires_regeneration' FROM generate_series(0,204)n`, id, f.template.Mappings[0].TargetRepositoryID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.db.Exec(`UPDATE nexus_migration_batches SET entry_count=205 WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	insert(f.template.ID)
	p := f.create(true)
	f.run()
	p = f.get(p.ID)
	insert(p.ActiveBatchID)
	if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET state='completed',scan_complete=true WHERE id=$1`, p.ActiveBatchID); err != nil {
		t.Fatal(err)
	}
	f.run()
	p = f.get(p.ID)
	if p.LastSuccessfulAt == nil || !p.LastSuccessfulAt.After(f.template.UpdatedAt) {
		t.Fatal("valid metadata pages did not reconcile")
	}
	if _, err := f.plans.Control(context.Background(), p.ID, p.Version, f.actor, uuid.NewString(), "stop", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE nexus_migration_entries SET identity='/unsupported/org/example/widget/1.0/widget-1.0.war.sha1',last_error='native_checksum_or_metadata_regenerated' WHERE batch_id=$1 AND ordinal=204`, f.template.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.plans.Create(context.Background(), m.ScheduleCreate{TemplateBatchID: f.template.ID, IntervalSeconds: 60}, f.actor, uuid.NewString()); err == nil || err.Error() != "successful_template_required" {
		t.Fatalf("checksum without its verified parent accepted: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE nexus_migration_entries SET identity='/unsupported/org/example/widget204/maven-metadata.xml.evil',last_error='maven_mixed_metadata_requires_regeneration' WHERE batch_id=$1 AND ordinal=204`, f.template.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.plans.Create(context.Background(), m.ScheduleCreate{TemplateBatchID: f.template.ID, IntervalSeconds: 60}, f.actor, uuid.NewString())
	if err == nil || err.Error() != "successful_template_required" {
		t.Fatalf("third-page unsupported lookalike accepted: %v", err)
	}
}
func (f *scheduleFixture) templateTime() *time.Time { return &f.template.UpdatedAt }

func TestPostgresMigrationScheduleLeaseRecoveryAndPauseFence(t *testing.T) {
	f := newScheduleFixture(t)
	p := f.create(true)
	old, err := f.ledger.ClaimDueSchedule(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec(`UPDATE nexus_migration_schedules SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	current, err := pgstore.New(f.db).ClaimDueSchedule(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if current.LeaseEpoch <= old.LeaseEpoch || current.LeaseToken == old.LeaseToken {
		t.Fatal("expired claim not fenced")
	}
	if err = f.ledger.DispatchSchedule(context.Background(), old, dispatchBatch(old), time.Now()); !errors.Is(err, m.ErrLeaseLost) {
		t.Fatalf("old dispatcher: %v", err)
	}
	paused, err := f.plans.Control(context.Background(), p.ID, current.Version, f.actor, "pause-key", "pause", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.ledger.DispatchSchedule(context.Background(), current, dispatchBatch(current), time.Now()); !errors.Is(err, m.ErrLeaseLost) {
		t.Fatalf("pause failed to fence dispatcher: %v", err)
	}
	replayed, err := f.plans.Control(context.Background(), p.ID, current.Version, f.actor, "pause-key", "pause", nil)
	if err != nil || replayed.Version != paused.Version {
		t.Fatalf("control replay: %+v %v", replayed, err)
	}
	if _, err = f.plans.Control(context.Background(), p.ID, current.Version, f.actor, uuid.NewString(), "stop", nil); !errors.Is(err, m.ErrVersionConflict) {
		t.Fatalf("CAS: %v", err)
	}
	if _, err = f.plans.Control(context.Background(), p.ID, paused.Version, f.actor, "pause-key", "stop", nil); !errors.Is(err, m.ErrIdempotencyConflict) {
		t.Fatalf("key mismatch: %v", err)
	}
	stopped, err := f.plans.Control(context.Background(), p.ID, paused.Version, f.actor, uuid.NewString(), "stop", nil)
	if err != nil {
		t.Fatal(err)
	}
	f.run()
	if stopped.NextRunAt != nil || f.count(p.ID) != 0 {
		t.Fatal("stopped plan dispatched")
	}
	e := f.evidence(time.Now().Add(-time.Millisecond))
	if _, err = f.plans.Control(context.Background(), p.ID, stopped.Version, f.actor, uuid.NewString(), "resume", &e); err == nil || err.Error() != "invalid_state" {
		t.Fatalf("stopped plan resumed: %v", err)
	}
}

func TestPostgresMigrationScheduleStartupDispatchesOneMissedOccurrence(t *testing.T) {
	f := newScheduleFixture(t)
	p := f.create(true)
	f.due(p.ID)
	ctx, cancel := context.WithCancel(context.Background())
	done := m.NewSchedules(pgstore.New(f.db), f.batches).StartScheduler(ctx, time.Hour)
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("scheduler did not stop")
		}
	}()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("startup did not dispatch overdue plan")
		case <-tick.C:
			if f.get(p.ID).ActiveBatchID != "" {
				if f.count(p.ID) != 1 {
					t.Fatal("missed periods created a backlog")
				}
				return
			}
		}
	}
}

func TestPostgresMigrationScheduleCapacityExpirySourcePolicyAndMappingFences(t *testing.T) {
	for _, kind := range []string{"expired", "source", "policy", "mapping", "active-manual"} {
		t.Run(kind, func(t *testing.T) {
			f := newScheduleFixture(t)
			p := f.create(true)
			if kind == "expired" {
				_, err := f.db.Exec(`UPDATE nexus_migration_schedules SET capacity_evidence=jsonb_set(capacity_evidence,'{validUntil}',to_jsonb($2::text)) WHERE id=$1`, p.ID, time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
				if err != nil {
					t.Fatal(err)
				}
				f.run()
				p = f.get(p.ID)
				if p.State != "waiting_capacity" || p.LastError != "capacity_evidence_expired" || f.count(p.ID) != 0 {
					t.Fatalf("expired: %+v", p)
				}
				return
			}
			if kind == "active-manual" {
				if _, err := f.batches.Start(context.Background(), f.template.StartInput, f.actor, uuid.NewString()); err != nil {
					t.Fatal(err)
				}
				f.run()
				p = f.get(p.ID)
				if p.State != "blocked" || p.LastError != "mapping_active_batch" || f.count(p.ID) != 0 {
					t.Fatalf("manual: %+v", p)
				}
				return
			}
			claim, err := f.ledger.ClaimDueSchedule(context.Background(), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			b := dispatchBatch(claim)
			want := "source_changed"
			switch kind {
			case "source":
				_, err = f.db.Exec(`UPDATE nexus_migration_sources SET version=version+1 WHERE id=$1`, p.SourceID)
			case "policy":
				b.PolicyVersion = "mutated-policy"
				want = "policy_version_changed"
			case "mapping":
				b.Mappings = []m.Mapping{{SourceRepository: "other", TargetRepositoryID: p.Mappings[0].TargetRepositoryID}}
				want = "policy_version_changed"
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = f.ledger.DispatchSchedule(context.Background(), claim, b, time.Now()); err == nil || err.Error() != want {
				t.Fatalf("fence: %v want %s", err, want)
			}
			if f.count(p.ID) != 0 {
				t.Fatal("invalid binding created a batch")
			}
		})
	}
}

func TestPostgresMigrationScheduleFailuresBoundedAndStoppedBatchOutcomeRetained(t *testing.T) {
	t.Run("bounded-dispatch", func(t *testing.T) {
		f := newScheduleFixture(t)
		p := f.create(true)
		f.publication.fail.Store(true)
		for attempt := 1; attempt <= 3; attempt++ {
			f.due(p.ID)
			f.run()
			p = f.get(p.ID)
			if p.DispatchAttempts != attempt || p.LastError != "scheduler_dispatch_failed" {
				t.Fatalf("attempt: %+v", p)
			}
		}
		if p.State != "blocked" || f.count(p.ID) != 0 {
			t.Fatalf("retry budget: %+v", p)
		}
		f.run()
		if f.get(p.ID).DispatchAttempts != 3 {
			t.Fatal("retry continued past bound")
		}
	})
	t.Run("retained-active-batch", func(t *testing.T) {
		f := newScheduleFixture(t)
		p := f.create(true)
		f.run()
		p = f.get(p.ID)
		active := p.ActiveBatchID
		p, err := f.plans.Control(context.Background(), p.ID, p.Version, f.actor, uuid.NewString(), "stop", nil)
		if err != nil {
			t.Fatal(err)
		}
		b, err := f.batches.Get(context.Background(), active)
		if err != nil || b.State != "scanning" {
			t.Fatalf("stop changed active batch: %+v %v", b, err)
		}
		if _, err = f.db.Exec(`UPDATE nexus_migration_batches SET state='completed',scan_complete=true,updated_at=clock_timestamp() WHERE id=$1`, active); err != nil {
			t.Fatal(err)
		}
		f.run()
		p = f.get(p.ID)
		if p.State != "stopped" || p.ActiveBatchID != "" || p.LastBatchID != active || p.LastSuccessfulAt == nil || !p.LastSuccessfulAt.After(f.template.UpdatedAt) {
			t.Fatalf("stopped outcome: %+v", p)
		}
	})
	t.Run("incomplete-completed-is-not-success", func(t *testing.T) {
		f := newScheduleFixture(t)
		p := f.create(true)
		f.run()
		p = f.get(p.ID)
		if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET state='completed' WHERE id=$1`, p.ActiveBatchID); err != nil {
			t.Fatal(err)
		}
		f.run()
		p = f.get(p.ID)
		if p.State != "blocked" || p.LastSuccessfulAt == nil || !p.LastSuccessfulAt.Equal(f.template.UpdatedAt) {
			t.Fatalf("false success: %+v", p)
		}
	})
}

func TestPostgresMigrationScheduleDispatchUsesClockAfterLockWaits(t *testing.T) {
	f := newScheduleFixture(t)
	now := time.Now().UTC()
	if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET capacity_evidence=jsonb_set(capacity_evidence,'{observedAt}',to_jsonb($2::text)) WHERE id=$1`, f.template.ID, now.Add(-50*time.Minute).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	e := f.evidence(now.Add(-30 * time.Minute))
	e.ValidUntil = now.Add(10 * time.Minute)
	p, err := f.plans.Create(context.Background(), m.ScheduleCreate{TemplateBatchID: f.template.ID, IntervalSeconds: 60, CapacityEvidence: &e}, f.actor, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	f.due(p.ID)
	if _, err = f.db.Exec(`UPDATE nexus_migration_schedules SET capacity_evidence=jsonb_set(capacity_evidence,'{validUntil}',to_jsonb($2::text)) WHERE id=$1`, p.ID, now.Add(-time.Second).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	claim, err := f.ledger.ClaimDueSchedule(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	// The caller's pre-wait timestamp is still inside the evidence window.
	// Only a fresh in-transaction clock check can reject this dispatch.
	err = f.ledger.DispatchSchedule(context.Background(), claim, dispatchBatch(claim), now.Add(-time.Minute))
	if err == nil || err.Error() != "capacity_evidence_expired" || f.count(p.ID) != 0 {
		t.Fatalf("old timestamp authorized expired evidence: %v", err)
	}
}

func TestPostgresMigrationScheduleResumeRequiresActiveBatchRecoveryFirst(t *testing.T) {
	f := newScheduleFixture(t)
	p := f.create(true)
	f.run()
	p = f.get(p.ID)
	if _, err := f.db.Exec(`UPDATE nexus_migration_batches SET state='blocked' WHERE id=$1`, p.ActiveBatchID); err != nil {
		t.Fatal(err)
	}
	f.run()
	p = f.get(p.ID)
	e := f.evidence(time.Now().Add(-time.Millisecond))
	_, err := f.plans.Control(context.Background(), p.ID, p.Version, f.actor, uuid.NewString(), "resume", &e)
	if err == nil || err.Error() != "active_batch_requires_attention" || f.count(p.ID) != 1 {
		t.Fatalf("plan bypassed active batch recovery: %v", err)
	}
}

func TestPostgresMigrationScheduleHTTPAuthorizationCASAndIdempotency(t *testing.T) {
	f := newScheduleFixture(t)
	store := repository.NewMemoryStore()
	h, err := app.NewGatewayHandler(app.GatewayOptions{Dependencies: app.Dependencies{MigrationService: f.batches, MigrationSchedules: f.plans}, Store: store, NPMMetadata: store, NPMRestore: store, Storage: app.SharedObjectStores(objectstore.NewMemoryStore()), Adapter: app.TestAdapter{}, Authenticator: app.Authenticator{AdminToken: "schedule-admin", AdminActor: f.actor, ResolverToken: "schedule-resolver"}})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, body any, version, key, token string) *httptest.ResponseRecorder {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if version != "" {
			r.Header.Set("If-Match", version)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	root := "/api/v2/migrations/nexus/schedules"
	for _, test := range []struct {
		token string
		want  int
	}{{"", 401}, {"schedule-resolver", 403}} {
		w := call("GET", root, nil, "", "", test.token)
		if w.Code != test.want {
			t.Fatalf("auth: %d %s", w.Code, w.Body.String())
		}
	}
	in := m.ScheduleCreate{TemplateBatchID: f.template.ID, IntervalSeconds: 60}
	w := call("POST", root, in, "", "create", "schedule-admin")
	if w.Code != 201 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var p m.Schedule
	if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.State != "waiting_capacity" || w.Header().Get("ETag") != p.Version {
		t.Fatalf("response: %+v", p)
	}
	replay := call("POST", root, in, "", "create", "schedule-admin")
	if replay.Code != 201 || !bytes.Equal(w.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	in.IntervalSeconds = 120
	if w = call("POST", root, in, "", "create", "schedule-admin"); w.Code != 409 {
		t.Fatalf("create conflict: %d %s", w.Code, w.Body.String())
	}
	path := root + "/" + p.ID
	if w = call("POST", path+"/pause", nil, "0", "stale", "schedule-admin"); w.Code != 412 {
		t.Fatalf("stale: %d %s", w.Code, w.Body.String())
	}
	if w = call("POST", path+"/pause", nil, p.Version, "pause", "schedule-admin"); w.Code != 200 {
		t.Fatalf("pause: %d %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	e := f.evidence(time.Now().Add(-time.Second))
	if w = call("POST", path+"/resume", map[string]any{"capacityEvidence": e}, p.Version, "resume", "schedule-admin"); w.Code != 200 {
		t.Fatalf("resume: %d %s", w.Code, w.Body.String())
	}
	if w = call("GET", root+"?limit=0", nil, "", "", "schedule-admin"); w.Code != 400 {
		t.Fatalf("page validation: %d", w.Code)
	}
	if w = call("GET", path, nil, "", "", "schedule-admin"); w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"active"`) {
		t.Fatalf("readback: %d %s", w.Code, w.Body.String())
	}
	if w = call("POST", root, map[string]any{"templateBatchId": f.template.ID, "intervalSeconds": 60, "unknown": "secret"}, "", "unknown", "schedule-admin"); w.Code != 400 || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("unknown fields: %d %s", w.Code, w.Body.String())
	}
}

func TestPostgresMigrationScheduledBatchUsesExistingRealByteWorker(t *testing.T) {
	f := newFixture(t, "raw", map[string][]byte{"scheduled.bin": []byte("first full content")})
	template := f.start()
	f.run(template)
	requireState(t, *template, "completed")
	plans := m.NewSchedules(f.ledger, f.service)
	e := f.evidence()
	p, err := plans.Create(context.Background(), m.ScheduleCreate{TemplateBatchID: template.ID, IntervalSeconds: 60, CapacityEvidence: &e}, "scheduled-byte-admin", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, cleanupErr := f.db.Exec(`UPDATE nexus_migration_schedules SET state='stopped',next_run_at=NULL WHERE id=$1`, p.ID)
		if cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	f.remote.mu.Lock()
	f.remote.files["added.bin"] = []byte("periodic new bytes")
	f.remote.mu.Unlock()
	if err = plans.RunDue(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	p, err = plans.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.ActiveBatchID == "" {
		t.Fatal("scheduler did not persist a batch")
	}
	if err = f.service.RunOnce(context.Background(), []string{"raw"}); err != nil {
		t.Fatal(err)
	}
	if err = plans.RunDue(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	b, err := f.service.Get(context.Background(), p.ActiveBatchID)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != "completed" || b.Counts.Committed != 1 || b.Counts.Skipped != 1 {
		t.Fatalf("incremental worker: %+v", b)
	}
	if _, err = f.native.GetRawAsset(context.Background(), f.repo.ID, "added.bin"); err != nil {
		t.Fatal(err)
	}
	updated, err := plans.Get(context.Background(), p.ID)
	if err != nil || updated.LastSuccessfulAt == nil || !updated.LastSuccessfulAt.After(template.UpdatedAt) {
		t.Fatalf("success readback: %+v %v", updated, err)
	}
}

func TestPostgresMigrationScheduleMavenRegenerationRetainsUnsupportedReport(t *testing.T) {
	pom := []byte(`<project><modelVersion>4.0.0</modelVersion><groupId>org.example</groupId><artifactId>widget</artifactId><version>1.0</version></project>`)
	f := newFixture(t, "maven2", map[string][]byte{
		"org/example/widget/1.0/widget-1.0.pom":      pom,
		"org/example/widget/1.0/widget-1.0.jar":      []byte("scheduled-maven-jar"),
		"org/example/widget/1.0/widget-1.0.jar.sha1": []byte("source sidecar is deliberately regenerated"),
		"org/example/widget/maven-metadata.xml":      []byte("source metadata is deliberately regenerated"),
	})
	template := f.start()
	f.run(template)
	requireState(t, *template, "completed")
	if template.Counts.Unsupported != 2 {
		t.Fatalf("regeneration report absent: %+v", template.Counts)
	}
	plans := m.NewSchedules(f.ledger, f.service)
	e := f.evidence()
	p, err := plans.Create(context.Background(), m.ScheduleCreate{TemplateBatchID: template.ID, IntervalSeconds: 60, CapacityEvidence: &e}, "scheduled-maven-admin", uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, cleanupErr := f.db.Exec(`UPDATE nexus_migration_schedules SET state='stopped',next_run_at=NULL WHERE id=$1`, p.ID)
		if cleanupErr != nil {
			t.Error(cleanupErr)
		}
	})
	if err = plans.RunDue(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	p, err = plans.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.service.RunOnce(context.Background(), []string{"maven"}); err != nil {
		t.Fatal(err)
	}
	if err = plans.RunDue(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	b, err := f.service.Get(context.Background(), p.ActiveBatchID)
	if err != nil {
		t.Fatal(err)
	}
	if b.State != "completed" || b.Counts.Unsupported != 2 || b.Counts.Skipped != 1 {
		t.Fatalf("report was lost or native scope failed: %+v", b)
	}
	updated, err := plans.Get(context.Background(), p.ID)
	if err != nil || updated.LastSuccessfulAt == nil || !updated.LastSuccessfulAt.After(template.UpdatedAt) {
		t.Fatalf("native scope success: %+v %v", updated, err)
	}
}
