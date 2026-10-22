package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/google/uuid"
)

var _ m.ScheduleStore = (*Store)(nil)

const scheduleColumns = `s.id::text,s.actor,s.request,s.version::text,s.state,s.next_run_at,s.last_successful_at,s.consumed_observed_at,s.capacity_evidence,COALESCE(s.active_batch_id::text,''),COALESCE(s.last_batch_id::text,''),s.last_error,s.dispatch_attempts,s.created_at,s.updated_at,s.lease_token,s.lease_epoch`

func scanSchedule(row scanner) (m.Schedule, error) {
	var p m.Schedule
	var request, evidence []byte
	err := row.Scan(&p.ID, &p.Actor, &request, &p.Version, &p.State, &p.NextRunAt, &p.LastSuccessfulAt, &p.ConsumedObservedAt, &evidence, &p.ActiveBatchID, &p.LastBatchID, &p.LastError, &p.DispatchAttempts, &p.CreatedAt, &p.UpdatedAt, &p.LeaseToken, &p.LeaseEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return p, m.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(request, &p.ScheduleBinding); err != nil {
		return p, err
	}
	if len(evidence) > 0 {
		err = json.Unmarshal(evidence, &p.CapacityEvidence)
	}
	return p, err
}

func (s *Store) GetSchedule(ctx context.Context, id string) (m.Schedule, error) {
	return scanSchedule(s.db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM nexus_migration_schedules s WHERE s.id::text=$1`, id))
}

func (s *Store) ListSchedules(ctx context.Context, limit int, cursor string) ([]m.Schedule, string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM nexus_migration_schedules s WHERE ($1='' OR s.id>NULLIF($1,'')::uuid) ORDER BY s.id LIMIT $2`, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	out := []m.Schedule{}
	for rows.Next() {
		p, e := scanSchedule(rows)
		if e != nil {
			return nil, "", e
		}
		out = append(out, p)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}

func (s *Store) ReplayScheduleCreate(ctx context.Context, actor, key, hash string) (m.Schedule, bool, error) {
	var id, stored string
	err := s.db.QueryRowContext(ctx, `SELECT id::text,request_hash FROM nexus_migration_schedules WHERE actor=$1 AND idempotency_key=$2`, actor, key).Scan(&id, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return m.Schedule{}, false, nil
	}
	if err != nil {
		return m.Schedule{}, false, err
	}
	if stored != hash {
		return m.Schedule{}, false, m.ErrIdempotencyConflict
	}
	p, err := s.GetSchedule(ctx, id)
	return p, true, err
}

func (s *Store) ReplayScheduleControl(ctx context.Context, id, actor, key, action, hash string) (m.Schedule, bool, error) {
	var operation, stored string
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT operation,request_hash,response FROM nexus_migration_schedule_controls WHERE schedule_id::text=$1 AND actor=$2 AND idempotency_key=$3`, id, actor, key).Scan(&operation, &stored, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return m.Schedule{}, false, nil
	}
	if err != nil {
		return m.Schedule{}, false, err
	}
	if operation != action || stored != hash {
		return m.Schedule{}, false, m.ErrIdempotencyConflict
	}
	var p m.Schedule
	err = json.Unmarshal(raw, &p)
	return p, true, err
}

// All schedule/manual batch dispatches use one short transaction lock before
// checking target overlap. Network scanning and copying never hold this lock.
func lockMigrationDispatch(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('nexus-migration-dispatch',0))`)
	return err
}

func activeMigrationTargets(ctx context.Context, tx *sql.Tx, mappings []m.Mapping, onlyScheduled bool) error {
	var busy bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM nexus_migration_batches b CROSS JOIN LATERAL jsonb_array_elements(b.request->'mappings') oldmp JOIN jsonb_array_elements($1::jsonb) newmp ON oldmp->>'targetRepositoryId'=newmp->>'targetRepositoryId' WHERE b.state NOT IN ('completed','failed','cancelled') AND (NOT $2 OR b.schedule_id IS NOT NULL))`, mustJSON(mappings), onlyScheduled).Scan(&busy)
	if err == nil && busy {
		return m.Failed("mapping_active_batch")
	}
	return err
}

// Recheck after transaction/source/target lock waits using the database clock.
// A timestamp captured before waiting cannot authorize expired evidence.
func freshScheduleEvidence(ctx context.Context, tx *sql.Tx, evidence *m.CapacityEvidence) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return now, err
	}
	if evidence == nil {
		return now, m.Failed("capacity_unknown")
	}
	if evidence.ObservedAt.IsZero() || evidence.ObservedAt.After(now) || !evidence.ValidUntil.After(now) || !evidence.ValidUntil.After(evidence.ObservedAt) || evidence.ValidUntil.Sub(evidence.ObservedAt) > time.Hour {
		return now, m.Failed("capacity_evidence_expired")
	}
	for _, value := range []*int64{evidence.FreeBytes, evidence.DownloadBytes, evidence.UploadBytes, evidence.BackupBytes, evidence.RestoreBytes, evidence.HeadroomBytes} {
		if value == nil {
			return now, m.Failed("capacity_unknown")
		}
		if *value < 0 {
			return now, m.Failed("invalid_request")
		}
	}
	return now, nil
}

func (s *Store) CreateSchedule(ctx context.Context, p m.Schedule, key, hash string) (m.Schedule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockMigrationDispatch(ctx, tx); err != nil {
		return p, err
	}
	var priorID, priorHash string
	err = tx.QueryRowContext(ctx, `SELECT id::text,request_hash FROM nexus_migration_schedules WHERE actor=$1 AND idempotency_key=$2`, p.Actor, key).Scan(&priorID, &priorHash)
	if err == nil {
		if priorHash != hash {
			return p, m.ErrIdempotencyConflict
		}
		return scanSchedule(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM nexus_migration_schedules s WHERE s.id::text=$1`, priorID))
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	var overlap bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM nexus_migration_schedules s CROSS JOIN LATERAL jsonb_array_elements(s.request->'mappings') oldmp JOIN jsonb_array_elements($1::jsonb) newmp ON oldmp->>'targetRepositoryId'=newmp->>'targetRepositoryId' WHERE s.state<>'stopped')`, mustJSON(p.Mappings)).Scan(&overlap)
	if err != nil {
		return p, err
	}
	if overlap {
		return p, m.Failed("mapping_schedule_exists")
	}
	template, err := scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM nexus_migration_batches b WHERE b.id::text=$1 FOR SHARE`, p.TemplateBatchID))
	if err != nil {
		return p, err
	}
	if template.State != "completed" || !template.ScanComplete || template.LastError != "" || template.SourceID != p.SourceID || template.SourceVersion != p.SourceVersion || template.PolicyVersion != p.PolicyVersion || string(mustJSON(template.Mappings)) != string(mustJSON(p.Mappings)) {
		return p, m.Failed("successful_template_required")
	}
	successful, err := scheduleEntriesSuccessful(ctx, tx, p.TemplateBatchID)
	if err != nil {
		return p, err
	}
	if !successful {
		return p, m.Failed("successful_template_required")
	}
	if err = lockSourceVersion(ctx, tx, p.SourceID, p.SourceVersion); err != nil {
		return p, err
	}
	if p.CapacityEvidence != nil {
		if _, err = freshScheduleEvidence(ctx, tx, p.CapacityEvidence); err != nil {
			return p, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO nexus_migration_schedules(id,actor,idempotency_key,request_hash,request,state,interval_seconds,next_run_at,last_successful_at,consumed_observed_at,capacity_evidence,last_batch_id,last_error) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,'')::uuid,$13)`, p.ID, p.Actor, key, hash, mustJSON(p.ScheduleBinding), p.State, p.IntervalSeconds, p.NextRunAt, p.LastSuccessfulAt, p.ConsumedObservedAt, mustJSON(p.CapacityEvidence), p.LastBatchID, p.LastError)
	if err != nil {
		return p, err
	}
	p, err = scanSchedule(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM nexus_migration_schedules s WHERE s.id::text=$1`, p.ID))
	if err != nil {
		return p, err
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	return p, nil
}

func (s *Store) ControlSchedule(ctx context.Context, id, expected, actor, key, action, hash string, evidence *m.CapacityEvidence, now time.Time) (m.Schedule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return m.Schedule{}, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := scanSchedule(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM nexus_migration_schedules s WHERE s.id::text=$1 FOR UPDATE`, id))
	if err != nil {
		return p, err
	}
	var storedAction, storedHash string
	var response []byte
	err = tx.QueryRowContext(ctx, `SELECT operation,request_hash,response FROM nexus_migration_schedule_controls WHERE schedule_id=$1 AND actor=$2 AND idempotency_key=$3`, id, actor, key).Scan(&storedAction, &storedHash, &response)
	if err == nil {
		if storedAction != action || storedHash != hash {
			return p, m.ErrIdempotencyConflict
		}
		err = json.Unmarshal(response, &p)
		return p, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if p.Version != expected {
		return p, m.ErrVersionConflict
	}
	if p.State == "stopped" {
		return p, m.Failed("invalid_state")
	}
	switch action {
	case "pause":
		if p.State == "paused" {
			return p, m.Failed("invalid_state")
		}
		p.State = "paused"
	case "stop":
		p.State, p.NextRunAt = "stopped", nil
	case "resume", "capacity":
		if evidence == nil {
			return p, m.Failed("capacity_unknown")
		}
		if action == "resume" && p.State != "paused" && p.State != "waiting_capacity" && p.State != "blocked" {
			return p, m.Failed("invalid_state")
		}
		if action == "resume" && p.ActiveBatchID != "" {
			var batchState string
			if err = tx.QueryRowContext(ctx, `SELECT state FROM nexus_migration_batches WHERE id=$1 FOR SHARE`, p.ActiveBatchID).Scan(&batchState); err != nil {
				return p, err
			}
			if batchState == "paused" || batchState == "pausing" || batchState == "blocked" {
				return p, m.Failed("active_batch_requires_attention")
			}
		}
		if p.ConsumedObservedAt != nil && !evidence.ObservedAt.After(*p.ConsumedObservedAt) || p.CapacityEvidence != nil && !evidence.ObservedAt.After(p.CapacityEvidence.ObservedAt) {
			return p, m.Failed("capacity_evidence_refresh_required")
		}
		if err = lockSourceVersion(ctx, tx, p.SourceID, p.SourceVersion); err != nil {
			return p, err
		}
		if _, err = freshScheduleEvidence(ctx, tx, evidence); err != nil {
			return p, err
		}
		p.CapacityEvidence, p.DispatchAttempts = evidence, 0
		if action == "resume" || p.State == "waiting_capacity" {
			p.State, p.LastError = "active", ""
		}
	default:
		return p, m.Failed("invalid_request")
	}
	_, err = tx.ExecContext(ctx, `UPDATE nexus_migration_schedules SET state=$2,version=version+1,next_run_at=$3,capacity_evidence=$4,last_error=$5,dispatch_attempts=$6,lease_token='',lease_expires_at=NULL,updated_at=$7 WHERE id=$1`, id, p.State, p.NextRunAt, mustJSON(p.CapacityEvidence), p.LastError, p.DispatchAttempts, now)
	if err != nil {
		return p, err
	}
	p, err = scanSchedule(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM nexus_migration_schedules s WHERE s.id::text=$1`, id))
	if err != nil {
		return p, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO nexus_migration_schedule_controls(schedule_id,actor,idempotency_key,operation,request_hash,response) VALUES($1,$2,$3,$4,$5,$6)`, id, actor, key, action, hash, mustJSON(p))
	if err != nil {
		return p, err
	}
	if err = tx.Commit(); err != nil {
		return p, err
	}
	return p, nil
}

// ScheduleTemplate reads only immutable batch configuration and terminal state.
// It deliberately avoids totals/entry/asset projections, including npm metadata.
func (s *Store) ScheduleTemplate(ctx context.Context, id string) (m.Batch, error) {
	return scanBatch(s.db.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM nexus_migration_batches b WHERE b.id::text=$1`, id))
}

// Completed/skipped entries pass by terminal state without loading any assets.
// Only the two Maven regeneration categories need path validation. Their narrow
// report rows and checksum references are processed in pages of at most 100;
// no Raw/npm body or private packument is deserialized or retained in memory.
func scheduleEntriesSuccessful(ctx context.Context, tx *sql.Tx, batchID string) (bool, error) {
	var rejected bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM nexus_migration_entries WHERE batch_id=$1 AND state NOT IN ('committed','skipped') AND NOT (state='unsupported' AND format='maven' AND last_error IN ('native_checksum_or_metadata_regenerated','maven_mixed_metadata_requires_regeneration')))`, batchID).Scan(&rejected); err != nil {
		return false, err
	}
	if rejected {
		return false, nil
	}
	type reference struct {
		Source   string `json:"source"`
		Target   string `json:"target"`
		Path     string `json:"path"`
		Identity string `json:"identity"`
	}
	cursor := -1
	for {
		rows, err := tx.QueryContext(ctx, `SELECT ordinal,source_repository,target_repository_id::text,identity,last_error,assets='[]'::jsonb FROM nexus_migration_entries WHERE batch_id=$1 AND ordinal>$2 AND state='unsupported' AND format='maven' AND last_error IN ('native_checksum_or_metadata_regenerated','maven_mixed_metadata_requires_regeneration') ORDER BY ordinal LIMIT 100`, batchID, cursor)
		if err != nil {
			return false, err
		}
		refs := make([]reference, 0, 100)
		count := 0
		for rows.Next() {
			e := m.Entry{Format: "maven", State: "unsupported"}
			var empty bool
			if err = rows.Scan(&cursor, &e.SourceRepository, &e.TargetRepositoryID, &e.Identity, &e.LastError, &empty); err != nil {
				_ = rows.Close()
				return false, err
			}
			if !empty {
				_ = rows.Close()
				return false, nil
			}
			original, identity, valid := m.MavenRegenerationReference(e)
			if !valid {
				_ = rows.Close()
				return false, nil
			}
			if original != "" {
				refs = append(refs, reference{Source: e.SourceRepository, Target: e.TargetRepositoryID, Path: original, Identity: identity})
			}
			count++
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return false, err
		}
		if len(refs) > 0 {
			var matched int
			err = tx.QueryRowContext(ctx, `SELECT count(*) FROM jsonb_to_recordset($2::jsonb) wanted(source text,target text,path text,identity text) WHERE EXISTS(SELECT 1 FROM nexus_migration_entries e WHERE e.batch_id=$1 AND e.format='maven' AND e.state IN ('committed','skipped') AND e.source_repository=wanted.source AND e.target_repository_id::text=wanted.target AND e.identity=wanted.identity AND EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN e.format='maven' THEN e.assets ELSE '[]'::jsonb END) a WHERE a->>'sourcePath'=wanted.path))`, batchID, mustJSON(refs)).Scan(&matched)
			if err != nil {
				return false, err
			}
			if matched != len(refs) {
				return false, nil
			}
		}
		if count < 100 {
			return true, nil
		}
	}
}

func (s *Store) ReconcileSchedules(ctx context.Context, limit int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT s.id::text,s.state,b.id::text,b.state,b.scan_complete,b.last_error,b.updated_at FROM nexus_migration_schedules s JOIN nexus_migration_batches b ON b.id=s.active_batch_id WHERE b.state IN ('completed','failed','cancelled','blocked','paused') AND (b.state IN ('completed','failed','cancelled') OR s.state='active') ORDER BY s.updated_at FOR UPDATE OF s,b SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return err
	}
	type outcome struct {
		id, state, batchID, batchState string
		complete                       bool
		message                        string
		at                             time.Time
	}
	completed := []outcome{}
	for rows.Next() {
		var o outcome
		if err = rows.Scan(&o.id, &o.state, &o.batchID, &o.batchState, &o.complete, &o.message, &o.at); err != nil {
			_ = rows.Close()
			return err
		}
		completed = append(completed, o)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, o := range completed {
		successful := false
		if o.batchState == "completed" && o.complete && o.message == "" {
			successful, err = scheduleEntriesSuccessful(ctx, tx, o.batchID)
			if err != nil {
				return err
			}
		}
		state, message := o.state, ""
		if !successful {
			message = "batch_requires_attention"
			if state != "paused" && state != "stopped" {
				state = "blocked"
			}
		}
		terminal := o.batchState == "completed" || o.batchState == "failed" || o.batchState == "cancelled"
		_, err = tx.ExecContext(ctx, `UPDATE nexus_migration_schedules SET active_batch_id=CASE WHEN $2 THEN NULL ELSE active_batch_id END,last_batch_id=$3,last_successful_at=CASE WHEN $4 THEN $5 ELSE last_successful_at END,state=$6,last_error=$7,version=version+1,lease_token='',lease_expires_at=NULL,updated_at=clock_timestamp() WHERE id=$1`, o.id, terminal, o.batchID, successful, o.at, state, message)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ClaimDueSchedule(ctx context.Context, now time.Time) (m.Schedule, error) {
	return scanSchedule(s.db.QueryRowContext(ctx, `WITH due AS (SELECT id FROM nexus_migration_schedules WHERE state='active' AND active_batch_id IS NULL AND next_run_at<=$1 AND (lease_token='' OR lease_expires_at<=clock_timestamp()) ORDER BY next_run_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE nexus_migration_schedules s SET lease_token=$2,lease_epoch=s.lease_epoch+1,lease_expires_at=clock_timestamp()+interval '30 seconds',version=s.version+1,updated_at=clock_timestamp() FROM due WHERE s.id=due.id RETURNING `+scheduleColumns, now, uuid.NewString()))
}

func (s *Store) DispatchSchedule(ctx context.Context, claim m.Schedule, b m.Batch, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockMigrationDispatch(ctx, tx); err != nil {
		return err
	}
	p, err := scanSchedule(tx.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM nexus_migration_schedules s WHERE s.id::text=$1 AND s.lease_token=$2 AND s.lease_expires_at>clock_timestamp() AND s.state='active' AND s.active_batch_id IS NULL FOR UPDATE`, claim.ID, claim.LeaseToken))
	if errors.Is(err, m.ErrNotFound) {
		return m.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if p.Version != claim.Version || p.LeaseEpoch != claim.LeaseEpoch || string(mustJSON(p.ScheduleBinding)) != string(mustJSON(claim.ScheduleBinding)) || string(mustJSON(p.CapacityEvidence)) != string(mustJSON(claim.CapacityEvidence)) {
		return m.ErrLeaseLost
	}
	if p.PolicyVersion != m.PolicyVersion || b.PolicyVersion != p.PolicyVersion || b.SourceID != p.SourceID || b.SourceVersion != p.SourceVersion || string(mustJSON(b.Mappings)) != string(mustJSON(p.Mappings)) {
		return m.Failed("policy_version_changed")
	}
	if p.CapacityEvidence == nil || p.ConsumedObservedAt != nil && !p.CapacityEvidence.ObservedAt.After(*p.ConsumedObservedAt) {
		return m.Failed("capacity_evidence_refresh_required")
	}
	if string(mustJSON(b.CapacityEvidence)) != string(mustJSON(p.CapacityEvidence)) {
		return m.Failed("capacity_evidence_expired")
	}
	if err = lockSourceVersion(ctx, tx, p.SourceID, p.SourceVersion); err != nil {
		return err
	}
	for _, mapping := range p.Mappings {
		var state, kind string
		err = tx.QueryRowContext(ctx, `SELECT state,repo_type FROM hosted_repositories WHERE id::text=$1 FOR SHARE`, mapping.TargetRepositoryID).Scan(&state, &kind)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (state != "active" || kind != "hosted") {
			return m.Failed("target_not_active_hosted")
		}
		if err != nil {
			return err
		}
	}
	if err = activeMigrationTargets(ctx, tx, p.Mappings, false); err != nil {
		return err
	}
	now, err = freshScheduleEvidence(ctx, tx, p.CapacityEvidence)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO nexus_migration_batches(id,actor,idempotency_key,request_hash,request,capacity_evidence,source_id,source_version,schedule_id,schedule_epoch) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, b.ID, p.Actor, "schedule:"+p.ID+":"+strconv.FormatInt(p.LeaseEpoch, 10), claim.Version, mustJSON(b.StartInput), mustJSON(b.CapacityEvidence), b.SourceID, b.SourceVersion, p.ID, p.LeaseEpoch)
	if err != nil {
		return err
	}
	next := now.Add(time.Duration(p.IntervalSeconds) * time.Second)
	_, err = tx.ExecContext(ctx, `UPDATE nexus_migration_schedules SET active_batch_id=$2,last_batch_id=$2,consumed_observed_at=$3,capacity_evidence=NULL,next_run_at=$4,last_error='',dispatch_attempts=0,lease_token='',lease_expires_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id=$1`, p.ID, b.ID, p.CapacityEvidence.ObservedAt, next)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) FailSchedule(ctx context.Context, p m.Schedule, state, message string, now time.Time) error {
	attempts := p.DispatchAttempts
	next := p.NextRunAt
	if state == "retry" {
		attempts++
		state = "active"
		if attempts >= m.MaxAttempts {
			state = "blocked"
		}
		retryAt := now.Add(time.Minute)
		next = &retryAt
	}
	result, err := s.db.ExecContext(ctx, `UPDATE nexus_migration_schedules SET state=$3,last_error=$4,dispatch_attempts=$5,next_run_at=$6,lease_token='',lease_expires_at=NULL,version=version+1,updated_at=clock_timestamp() WHERE id::text=$1 AND lease_token=$2 AND lease_expires_at>clock_timestamp() AND state='active'`, p.ID, p.LeaseToken, state, message, attempts, next)
	return changed(result, err)
}
