// Package pgstore implements the migration ledger and its publication fence.
package pgstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	m "github.com/ccsert/artifact-gateway/internal/nexusmigration"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

var _ m.Store = (*Store)(nil)

type scanner interface{ Scan(...any) error }

const batchColumns = `b.id::text,b.actor,b.request,b.capacity_evidence,b.state,b.version::text,b.manifest_sha256,b.scan_complete,b.metadata_discovered,b.source_verification_bytes,b.entry_count,b.total_bytes,b.last_error,b.last_scan_failure,b.created_at,b.updated_at,b.lease_token,b.lease_epoch,b.pause_requested,b.cancel_requested`

func scanBatch(row scanner) (m.Batch, error) {
	var b m.Batch
	var request, evidence, failure []byte
	err := row.Scan(&b.ID, &b.Actor, &request, &evidence, &b.State, &b.Version, &b.ManifestSHA256, &b.ScanComplete, &b.MetadataDiscovered, &b.SourceVerificationBytes, &b.EntryCount, &b.TotalBytes, &b.LastError, &failure, &b.CreatedAt, &b.UpdatedAt, &b.LeaseToken, &b.LeaseEpoch, &b.PauseRequested, &b.CancelRequested)
	if errors.Is(err, sql.ErrNoRows) {
		return b, m.ErrNotFound
	}
	if err != nil {
		return b, err
	}
	if err = json.Unmarshal(request, &b.StartInput); err != nil {
		return b, err
	}
	if len(failure) > 0 {
		if err = json.Unmarshal(failure, &b.LastScanFailure); err != nil {
			return b, err
		}
	}
	err = json.Unmarshal(evidence, &b.CapacityEvidence)
	return b, err
}
func (s *Store) totals(ctx context.Context, b *m.Batch) error {
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(sum(transferred_bytes),0),COALESCE(sum(verified_bytes),0),COALESCE(sum(visible_identities),0),
	 count(*) FILTER(WHERE state='pending'),count(*) FILTER(WHERE state='uploaded'),count(*) FILTER(WHERE state='verified'),count(*) FILTER(WHERE state='committed'),count(*) FILTER(WHERE state='skipped'),count(*) FILTER(WHERE state='conflict'),count(*) FILTER(WHERE state='failed'),count(*) FILTER(WHERE state='unsupported'),count(*) FILTER(WHERE state='source_missing'),count(*) FILTER(WHERE state='source_deleted') FROM nexus_migration_entries WHERE batch_id=$1`, b.ID).Scan(&b.TransferredBytes, &b.VerifiedBytes, &b.VisibleIdentities, &b.Counts.Pending, &b.Counts.Uploaded, &b.Counts.Verified, &b.Counts.Committed, &b.Counts.Skipped, &b.Counts.Conflict, &b.Counts.Failed, &b.Counts.Unsupported, &b.Counts.SourceMissing, &b.Counts.SourceDeleted)
	if err != nil {
		return err
	}
	return s.db.QueryRowContext(ctx, `SELECT COALESCE(sum(size),0) FROM (SELECT DISTINCT e.format,a->>'sha256' AS sha256,(a->>'size')::bigint AS size FROM nexus_migration_entries e CROSS JOIN LATERAL jsonb_array_elements(e.assets) a WHERE e.batch_id=$1 AND a->>'verified'='true' AND e.state IN ('verified','committed','skipped')) verified`, b.ID).Scan(&b.VerifiedBytes)
}
func (s *Store) Create(ctx context.Context, b m.Batch, key, hash string) (m.Batch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return m.Batch{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = lockMigrationDispatch(ctx, tx); err != nil {
		return m.Batch{}, err
	}
	var priorID, priorHash string
	err = tx.QueryRowContext(ctx, `SELECT id::text,request_hash FROM nexus_migration_batches WHERE actor=$1 AND idempotency_key=$2`, b.Actor, key).Scan(&priorID, &priorHash)
	if err == nil {
		if priorHash != hash {
			return m.Batch{}, m.ErrIdempotencyConflict
		}
		if err = tx.Rollback(); err != nil {
			return m.Batch{}, err
		}
		return s.Get(ctx, priorID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return m.Batch{}, err
	}
	// Manual starts may not overlap a live batch already owned by a plan.
	// Plan dispatch checks all live batches under this same transaction lock.
	if err = activeMigrationTargets(ctx, tx, b.Mappings, true, ""); err != nil {
		return m.Batch{}, err
	}
	request, _ := json.Marshal(b.StartInput)
	evidence, _ := json.Marshal(b.CapacityEvidence)
	_, err = tx.ExecContext(ctx, `INSERT INTO nexus_migration_batches(id,actor,idempotency_key,request_hash,request,capacity_evidence,source_id,source_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, b.ID, b.Actor, key, hash, request, evidence, b.SourceID, b.SourceVersion)
	if err != nil {
		return m.Batch{}, err
	}
	if err = tx.Commit(); err != nil {
		return m.Batch{}, err
	}
	return s.Get(ctx, b.ID)
}
func (s *Store) Get(ctx context.Context, id string) (m.Batch, error) {
	b, err := scanBatch(s.db.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM nexus_migration_batches b WHERE b.id::text=$1`, id))
	if err == nil {
		err = s.totals(ctx, &b)
	}
	return b, err
}
func (s *Store) List(ctx context.Context, limit int, cursor string) ([]m.Batch, string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+batchColumns+` FROM nexus_migration_batches b WHERE ($1='' OR b.id>NULLIF($1,'')::uuid) ORDER BY b.id LIMIT $2`, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	out := []m.Batch{}
	for rows.Next() {
		b, e := scanBatch(rows)
		if e != nil {
			_ = rows.Close()
			return nil, "", e
		}
		out = append(out, b)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].ID
	}
	for i := range out {
		if err = s.totals(ctx, &out[i]); err != nil {
			return nil, "", err
		}
	}
	return out, next, nil
}

// The public asset schema deliberately omits internal, derived checksum facts.
type savedAsset struct {
	m.Asset
	SHA1         string `json:"sha1"`
	MD5          string `json:"md5"`
	SHA512       string `json:"sha512,omitempty"`
	NPMPackument []byte `json:"npmPackument,omitempty"`
}

func encodeAssets(in []m.Asset) []byte {
	out := make([]savedAsset, len(in))
	for i, a := range in {
		out[i] = savedAsset{a, a.SHA1, a.MD5, a.SHA512, a.NPMPackument}
	}
	b, _ := json.Marshal(out)
	return b
}

const entryColumns = `id::text,batch_id::text,source_repository,target_repository_id::text,format,identity,state,attempts,assets,session_id,transferred_bytes,verified_bytes,visible_identities,last_error,updated_at`

func scanEntry(row scanner) (m.Entry, error) {
	var e m.Entry
	var raw []byte
	err := row.Scan(&e.ID, &e.BatchID, &e.SourceRepository, &e.TargetRepositoryID, &e.Format, &e.Identity, &e.State, &e.Attempts, &raw, &e.SessionID, &e.TransferredBytes, &e.VerifiedBytes, &e.VisibleIdentities, &e.LastError, &e.UpdatedAt)
	if err != nil {
		return e, err
	}
	var assets []savedAsset
	if err = json.Unmarshal(raw, &assets); err != nil {
		return e, err
	}
	e.Assets = make([]m.Asset, len(assets))
	for i, a := range assets {
		e.Assets[i] = a.Asset
		e.Assets[i].SHA1 = a.SHA1
		e.Assets[i].MD5 = a.MD5
		e.Assets[i].SHA512 = a.SHA512
		e.Assets[i].NPMPackument = a.NPMPackument
	}
	return e, nil
}
func (s *Store) Entries(ctx context.Context, id string, limit int, cursor string) ([]m.Entry, string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+entryColumns+` FROM nexus_migration_entries WHERE batch_id::text=$1 AND ($2='' OR id>NULLIF($2,'')::uuid) ORDER BY id LIMIT $3`, id, cursor, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rows.Close() }()
	out := []m.Entry{}
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, e)
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
func (s *Store) PreviousEntries(ctx context.Context, b m.Batch) ([]m.Entry, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id::text FROM nexus_migration_batches WHERE id<>$1 AND source_id=$2 AND scan_complete AND created_at<$3 AND request->'mappings'=$4::jsonb AND source_version=$5 AND request->>'policyVersion'=$6 ORDER BY created_at DESC LIMIT 1`, b.ID, b.SourceID, b.CreatedAt, mustJSON(b.Mappings), b.SourceVersion, b.PolicyVersion).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return []m.Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []m.Entry{}
	cursor := ""
	for {
		page, next, err := s.Entries(ctx, id, 100, cursor)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (s *Store) Control(ctx context.Context, id, expected, actor, key, action, hash string, evidence *m.CapacityEvidence) (m.Batch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return m.Batch{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// Reactivation follows dispatch -> publication lease -> batch row. Taking
	// dispatch after the batch row would invert schedule creation's lock order.
	if action == "resume" || action == "retry" {
		if err = lockMigrationDispatch(ctx, tx); err != nil {
			return m.Batch{}, err
		}
	}
	// Cancellation takes the publication fence. A committed entry is retained;
	// once cancellation returns no old worker can cross the publication boundary.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('nexus-migration-lease:'||$1,0))`, id); err != nil {
		return m.Batch{}, err
	}
	b, err := scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM nexus_migration_batches b WHERE b.id::text=$1 FOR UPDATE`, id))
	if err != nil {
		return b, err
	}
	var storedAction, storedHash string
	var response []byte
	err = tx.QueryRowContext(ctx, `SELECT operation,request_hash,response FROM nexus_migration_controls WHERE batch_id=$1 AND actor=$2 AND idempotency_key=$3`, id, actor, key).Scan(&storedAction, &storedHash, &response)
	if err == nil {
		if storedAction != action || storedHash != hash {
			return b, m.ErrIdempotencyConflict
		}
		err = json.Unmarshal(response, &b)
		return b, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}
	if b.Version != expected {
		return b, m.ErrVersionConflict
	}
	switch action {
	case "pause":
		if b.State != "scanning" && b.State != "ready" && b.State != "running" {
			return b, m.Failed("invalid_state")
		}
		b.PauseRequested = true
		b.State = "pausing"
		if b.LeaseToken == "" {
			b.State = "paused"
		}
	case "cancel":
		if b.State == "completed" || b.State == "cancelled" {
			return b, m.Failed("invalid_state")
		}
		b.CancelRequested = true
		b.State = "cancelled"
		b.LeaseToken = ""
	case "resume", "retry":
		if action == "resume" && b.State != "paused" && b.State != "blocked" {
			return b, m.Failed("invalid_state")
		}
		if action == "retry" && b.State != "failed" && b.State != "completed" && b.State != "blocked" {
			return b, m.Failed("invalid_state")
		}
		if evidence == nil {
			return b, m.Failed("capacity_unknown")
		}
		if err = activeMigrationTargets(ctx, tx, b.Mappings, false, b.ID); err != nil {
			return b, err
		}
		b.CapacityEvidence = *evidence
		b.PauseRequested = false
		b.CancelRequested = false
		b.LeaseToken = ""
		b.State = "ready"
		if !b.ScanComplete {
			b.State = "scanning"
		}
		if action == "retry" {
			if _, err = tx.ExecContext(ctx, `UPDATE nexus_migration_entries SET state='pending',last_error='' WHERE batch_id=$1 AND state IN ('failed','source_missing') AND attempts<3`, id); err != nil {
				return b, err
			}
		}
	default:
		return b, m.Failed("invalid_request")
	}
	_, err = tx.ExecContext(ctx, `UPDATE nexus_migration_batches SET state=$2,version=version+1,capacity_evidence=$3,pause_requested=$4,cancel_requested=$5,lease_token=$6,lease_expires_at=CASE WHEN $6='' THEN NULL ELSE lease_expires_at END,last_error='',updated_at=clock_timestamp() WHERE id=$1`, id, b.State, mustJSON(b.CapacityEvidence), b.PauseRequested, b.CancelRequested, b.LeaseToken)
	if err != nil {
		return b, err
	}
	v, _ := strconv.ParseInt(b.Version, 10, 64)
	b.Version = strconv.FormatInt(v+1, 10)
	b.UpdatedAt = time.Now().UTC()
	if err = s.totals(ctx, &b); err != nil {
		return b, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO nexus_migration_controls(batch_id,actor,idempotency_key,operation,request_hash,response) VALUES($1,$2,$3,$4,$5,$6)`, id, actor, key, action, hash, mustJSON(b)); err != nil {
		return b, err
	}
	if err = tx.Commit(); err != nil {
		return b, err
	}
	return b, nil
}
func (s *Store) Claim(ctx context.Context, formats []string) (m.Batch, error) {
	if len(formats) == 0 {
		return m.Batch{}, m.ErrNotFound
	}
	// Recovery owns the same fence as scan receipts and native publication. A
	// partial scan resumes its private checkpoint; it never becomes ready.
	_, err := s.db.ExecContext(ctx, `WITH expired AS (SELECT id FROM nexus_migration_batches WHERE lease_token<>'' AND lease_expires_at<=clock_timestamp() AND pg_try_advisory_xact_lock(hashtextextended('nexus-migration-lease:'||id::text,0)) FOR UPDATE SKIP LOCKED) UPDATE nexus_migration_batches b SET state=CASE WHEN b.cancel_requested THEN 'cancelled' WHEN b.pause_requested THEN 'paused' WHEN NOT b.scan_complete THEN 'scanning' ELSE 'ready' END,lease_token='',lease_expires_at=NULL,last_error=b.last_error,version=version+1,updated_at=clock_timestamp() FROM expired WHERE b.id=expired.id`)
	if err != nil {
		return m.Batch{}, err
	}
	// A batch is claimed only by a node admitting all of its target formats.
	return scanBatch(s.db.QueryRowContext(ctx, `WITH candidate AS (SELECT b.id FROM nexus_migration_batches b WHERE b.state IN ('scanning','ready') AND b.lease_token='' AND NOT b.pause_requested AND NOT b.cancel_requested AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(b.request->'mappings') mp JOIN hosted_repositories r ON r.id::text=mp->>'targetRepositoryId' WHERE NOT (r.format=ANY($1))) ORDER BY b.created_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE nexus_migration_batches b SET state='running',lease_token=$2,lease_epoch=lease_epoch+1,lease_expires_at=clock_timestamp()+interval '2 minutes',version=version+1,updated_at=clock_timestamp() FROM candidate WHERE b.id=candidate.id RETURNING `+batchColumns, formats, uuid.NewString()))
}
func (s *Store) Renew(ctx context.Context, id, token string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET lease_expires_at=clock_timestamp()+interval '2 minutes' WHERE id::text=$1 AND lease_token=$2 AND lease_expires_at>clock_timestamp() AND state IN ('running','pausing') AND NOT cancel_requested`, id, token)
	return changed(result, err)
}
func changed(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return m.ErrLeaseLost
	}
	return nil
}
func validLease(ctx context.Context, tx *sql.Tx, id, token string) error {
	var ok bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM nexus_migration_batches WHERE id::text=$1 AND lease_token=$2 AND lease_expires_at>clock_timestamp() AND state IN ('running','pausing'))`, id, token).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return m.ErrLeaseLost
	}
	return nil
}
func (s *Store) Seal(ctx context.Context, b m.Batch, entries []m.Entry, hash string, metadata, total int64) error {
	tx, err := s.inventoryTransaction(ctx, b)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	scan, err := readInventoryScan(ctx, tx, b.ID)
	if err != nil {
		return err
	}
	if !scan.Verified() || metadata != scan.AssetCount {
		return m.Failed("source_scan_incomplete")
	}
	if scan.BindingSHA256 != m.InventoryBinding(b, scan.Repositories) {
		return m.Failed("source_changed")
	}
	for i, e := range entries {
		if _, err = tx.ExecContext(ctx, `INSERT INTO nexus_migration_entries(id,batch_id,ordinal,source_repository,target_repository_id,format,identity,state,assets,last_error) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, e.ID, b.ID, i, e.SourceRepository, e.TargetRepositoryID, e.Format, e.Identity, e.State, encodeAssets(e.Assets), e.LastError); err != nil {
			return err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE nexus_migration_batches SET manifest_sha256=$3,scan_complete=true,metadata_discovered=$4,entry_count=$5,total_bytes=$6,last_scan_failure=NULL,version=version+1,updated_at=clock_timestamp() WHERE id::text=$1 AND lease_token=$2 AND lease_expires_at>clock_timestamp() AND NOT scan_complete`, b.ID, b.LeaseToken, hash, metadata, len(entries), total)
	if err = changed(result, err); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Checkpoint(ctx context.Context, e m.Entry, token string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE nexus_migration_entries e SET state=$4,assets=$5,session_id=$6,attempts=$7,verified_bytes=$8,visible_identities=$9,last_error=$10,updated_at=clock_timestamp() FROM nexus_migration_batches b WHERE e.batch_id=b.id AND e.id::text=$1 AND b.id::text=$2 AND b.lease_token=$3 AND b.lease_expires_at>clock_timestamp() AND b.state IN ('running','pausing') AND (SELECT jsonb_agg(a-'uploaded'-'verified') FROM jsonb_array_elements(e.assets) a)=(SELECT jsonb_agg(a-'uploaded'-'verified') FROM jsonb_array_elements($5::jsonb) a)`, e.ID, e.BatchID, token, e.State, encodeAssets(e.Assets), e.SessionID, e.Attempts, e.VerifiedBytes, e.VisibleIdentities, e.LastError)
	return changed(result, err)
}
func (s *Store) AddTransferred(ctx context.Context, id, batch, token string, n int64) error {
	if n < 0 {
		return m.Failed("invalid_request")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE nexus_migration_entries e SET transferred_bytes=e.transferred_bytes+$4,updated_at=clock_timestamp() FROM nexus_migration_batches b WHERE e.batch_id=b.id AND e.id::text=$1 AND b.id::text=$2 AND b.lease_token=$3 AND b.lease_expires_at>clock_timestamp() AND b.state IN ('running','pausing')`, id, batch, token, n)
	return changed(result, err)
}
func (s *Store) PublicationContext(ctx context.Context, id, token string) context.Context {
	return repository.WithNativePublicationGuard(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// Recovery and controls take this same key. It is owned by the native
		// transaction itself: losing its backend also aborts native publication.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('nexus-migration-lease:'||$1,0))`, id); err != nil {
			return err
		}
		var valid bool
		var sourceID, sourceVersion string
		err := tx.QueryRowContext(ctx, `SELECT lease_token=$2 AND lease_expires_at>clock_timestamp() AND state IN ('running','pausing') AND NOT cancel_requested,source_id::text,source_version FROM nexus_migration_batches WHERE id::text=$1 FOR UPDATE`, id, token).Scan(&valid, &sourceID, &sourceVersion)
		if err != nil {
			return err
		}
		if !valid {
			return m.ErrLeaseLost
		}
		if err = lockSourceVersion(ctx, tx, sourceID, sourceVersion); err != nil {
			return err
		}
		// Waiting for a source writer may outlive the initial owner check.
		return validLease(ctx, tx, id, token)
	})
}
func (s *Store) Finish(ctx context.Context, id, token, state, message string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET state=CASE WHEN cancel_requested THEN 'cancelled' WHEN pause_requested THEN 'paused' ELSE $3 END,lease_token='',lease_expires_at=NULL,last_error=$4,version=version+1,updated_at=clock_timestamp() WHERE id::text=$1 AND lease_token=$2 AND lease_expires_at>clock_timestamp()`, id, token, state, message)
	return changed(result, err)
}

func (s *Store) ReplayStart(ctx context.Context, actor, key, hash string) (m.Batch, bool, error) {
	var id, stored string
	err := s.db.QueryRowContext(ctx, `SELECT id::text,request_hash FROM nexus_migration_batches WHERE actor=$1 AND idempotency_key=$2`, actor, key).Scan(&id, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return m.Batch{}, false, nil
	}
	if err != nil {
		return m.Batch{}, false, err
	}
	if stored != hash {
		return m.Batch{}, true, m.ErrIdempotencyConflict
	}
	b, err := s.Get(ctx, id)
	return b, true, err
}

// BeginEntry serializes the in-flight declaration with pause/cancel. A worker
// cannot begin native I/O after a pause has been acknowledged.
func (s *Store) BeginEntry(ctx context.Context, e m.Entry, token string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var valid, pause, cancel bool
	err = tx.QueryRowContext(ctx, `SELECT lease_token=$2 AND lease_expires_at>clock_timestamp() AND state IN ('running','pausing'),pause_requested,cancel_requested FROM nexus_migration_batches WHERE id::text=$1 FOR UPDATE`, e.BatchID, token).Scan(&valid, &pause, &cancel)
	if err != nil {
		return err
	}
	if !valid {
		return m.ErrLeaseLost
	}
	if pause || cancel {
		return m.ErrStopped
	}
	result, err := tx.ExecContext(ctx, `UPDATE nexus_migration_entries SET attempts=$2,updated_at=clock_timestamp() WHERE id::text=$1 AND batch_id::text=$3`, e.ID, e.Attempts, e.BatchID)
	if err = changed(result, err); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RecordScanProgress(ctx context.Context, id, token string, metadata, bytes int64) error {
	if metadata < 0 || bytes < 0 {
		return m.Failed("invalid_request")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET metadata_discovered=GREATEST(metadata_discovered,$3),source_verification_bytes=source_verification_bytes+$4,updated_at=clock_timestamp() WHERE id::text=$1 AND lease_token=$2 AND lease_expires_at>clock_timestamp() AND state IN ('running','pausing')`, id, token, metadata, bytes)
	return changed(result, err)
}

func (s *Store) RecordScanFailure(ctx context.Context, id, token string, failure m.ScanFailure) error {
	result, err := s.db.ExecContext(ctx, `UPDATE nexus_migration_batches SET last_scan_failure=$3,version=version+1,updated_at=clock_timestamp() WHERE id::text=$1 AND lease_token=$2 AND lease_expires_at>clock_timestamp() AND state IN ('running','pausing') AND NOT scan_complete`, id, token, mustJSON(failure))
	return changed(result, err)
}

func (s *Store) ReplayControl(ctx context.Context, id, actor, key, action, hash string) (m.Batch, bool, error) {
	var storedAction, storedHash string
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT operation,request_hash,response FROM nexus_migration_controls WHERE batch_id::text=$1 AND actor=$2 AND idempotency_key=$3`, id, actor, key).Scan(&storedAction, &storedHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return m.Batch{}, false, nil
	}
	if err != nil {
		return m.Batch{}, false, err
	}
	if storedAction != action || storedHash != hash {
		return m.Batch{}, true, m.ErrIdempotencyConflict
	}
	var b m.Batch
	err = json.Unmarshal(raw, &b)
	return b, true, err
}
