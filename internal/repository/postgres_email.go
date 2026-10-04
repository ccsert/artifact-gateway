package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
)

const emailTargetColumns = `id::text,name,locale,recipient_ciphertext,enabled,version::text,created_at,updated_at`

func scanEmailTarget(row interface{ Scan(...any) error }) (EmailTarget, error) {
	var v EmailTarget
	err := row.Scan(&v.ID, &v.Name, &v.Locale, &v.RecipientCiphertext, &v.Enabled, &v.Version, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return v, err
}
func (s *PostgresStore) CreateEmailTarget(ctx context.Context, v EmailTarget) (EmailTarget, error) {
	return scanEmailTarget(s.db.QueryRowContext(ctx, `INSERT INTO email_targets(id,name,locale,recipient_ciphertext,enabled) VALUES ($1,$2,$3,$4,$5) RETURNING `+emailTargetColumns, v.ID, v.Name, v.Locale, v.RecipientCiphertext, v.Enabled))
}
func (s *PostgresStore) GetEmailTarget(ctx context.Context, id string) (EmailTarget, error) {
	return scanEmailTarget(s.db.QueryRowContext(ctx, `SELECT `+emailTargetColumns+` FROM email_targets WHERE id::text=$1`, id))
}
func (s *PostgresStore) ListEmailTargets(ctx context.Context) ([]EmailTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+emailTargetColumns+` FROM email_targets ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]EmailTarget, 0)
	for rows.Next() {
		v, e := scanEmailTarget(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *PostgresStore) UpdateEmailTarget(ctx context.Context, v EmailTarget, version string) (EmailTarget, error) {
	out, err := scanEmailTarget(s.db.QueryRowContext(ctx, `UPDATE email_targets SET name=$2,locale=$3,recipient_ciphertext=$4,enabled=$5,version=version+1,updated_at=clock_timestamp() WHERE id::text=$1 AND version::text=$6 RETURNING `+emailTargetColumns, v.ID, v.Name, v.Locale, v.RecipientCiphertext, v.Enabled, version))
	if errors.Is(err, ErrNotFound) {
		if _, e := s.GetEmailTarget(ctx, v.ID); e == nil {
			err = ErrVersionConflict
		}
	}
	return out, err
}

const emailDeliveryColumns = `id::text,event_id::text,request_key::text,target_id::text,target_version::text,scenario,locale,template_version,recipient_ciphertext,from_address,console_origin,state,attempts,possible_duplicate,version::text,error_code,lease_owner,COALESCE(lease_token::text,''),lease_expires_at,next_attempt_at,accepted_at,created_at,updated_at,kind,COALESCE(quota_rule_id::text,''),COALESCE(episode_id::text,''),COALESCE(event_sequence,0),quota_descriptor,automatic_cancellation_code`

func scanEmailDelivery(row interface{ Scan(...any) error }) (EmailDelivery, error) {
	var v EmailDelivery
	var expiry, accepted sql.NullTime
	var descriptor []byte
	err := row.Scan(&v.ID, &v.EventID, &v.RequestKey, &v.TargetID, &v.TargetVersion, &v.Scenario, &v.Locale, &v.TemplateVersion, &v.RecipientCiphertext, &v.From, &v.ConsoleOrigin, &v.State, &v.Attempts, &v.PossibleDuplicate, &v.Version, &v.ErrorCode, &v.LeaseOwner, &v.LeaseToken, &expiry, &v.NextAttemptAt, &accepted, &v.CreatedAt, &v.UpdatedAt, &v.Kind, &v.QuotaRuleID, &v.EpisodeID, &v.EventSequence, &descriptor, &v.AutomaticCancellationCode)
	if err == nil && len(descriptor) > 0 {
		err = json.Unmarshal(descriptor, &v.QuotaEvent)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	if expiry.Valid {
		v.LeaseExpiresAt = expiry.Time
	}
	if accepted.Valid {
		v.AcceptedAt = accepted.Time
	}
	return v, err
}
func emailRatePostgres(ctx context.Context, tx *sql.Tx) error {
	// Shared transaction lock serializes first submissions and replays across API instances.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(15112,229)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM email_test_requests WHERE created_at<=clock_timestamp()-interval '1 minute'`); err != nil {
		return err
	}
	var recent, active int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM email_test_requests`).Scan(&recent); err != nil {
		return err
	}
	if recent >= 5 {
		return ErrEmailRateLimited
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM email_test_deliveries WHERE state IN ('pending','retrying','delivering')`).Scan(&active); err != nil {
		return err
	}
	if active >= 1000 {
		return ErrEmailQueueFull
	}
	return nil
}
func (s *PostgresStore) EnqueueEmailTest(ctx context.Context, r EmailTestRequest) (EmailDelivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EmailDelivery{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(15112,229)`); err != nil {
		return EmailDelivery{}, err
	}
	existing, e := scanEmailDelivery(tx.QueryRowContext(ctx, `SELECT `+emailDeliveryColumns+` FROM email_test_deliveries WHERE request_key::text=$1`, r.RequestKey))
	if e == nil {
		if !emailSameRequest(existing, r) {
			return EmailDelivery{}, ErrEmailIdempotencyConflict
		}
		return existing, nil
	}
	if !errors.Is(e, ErrNotFound) {
		return EmailDelivery{}, e
	}
	target, err := scanEmailTarget(tx.QueryRowContext(ctx, `SELECT `+emailTargetColumns+` FROM email_targets WHERE id::text=$1 FOR SHARE`, r.TargetID))
	if err != nil {
		return EmailDelivery{}, err
	}
	if target.Version != r.TargetVersion {
		return EmailDelivery{}, ErrVersionConflict
	}
	if !target.Enabled {
		return EmailDelivery{}, ErrEmailTargetDisabled
	}
	if err = emailRatePostgres(ctx, tx); err != nil {
		return EmailDelivery{}, err
	}
	v, err := scanEmailDelivery(tx.QueryRowContext(ctx, `INSERT INTO email_test_deliveries(id,event_id,request_key,target_id,target_version,scenario,locale,template_version,recipient_ciphertext,from_address,console_origin) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING `+emailDeliveryColumns, r.ID, r.EventID, r.RequestKey, target.ID, target.Version, r.Scenario, target.Locale, r.TemplateVersion, target.RecipientCiphertext, r.From, r.ConsoleOrigin))
	if err != nil {
		return EmailDelivery{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO email_test_requests DEFAULT VALUES`); err != nil {
		return EmailDelivery{}, err
	}
	return v, tx.Commit()
}
func (s *PostgresStore) GetEmailDelivery(ctx context.Context, id string) (EmailDelivery, error) {
	return scanEmailDelivery(s.db.QueryRowContext(ctx, `SELECT `+emailDeliveryColumns+` FROM email_test_deliveries WHERE id::text=$1`, id))
}
func (s *PostgresStore) ListEmailDeliveries(ctx context.Context, limit int) ([]EmailDelivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+emailDeliveryColumns+` FROM email_test_deliveries ORDER BY created_at DESC,id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]EmailDelivery, 0)
	for rows.Next() {
		v, e := scanEmailDelivery(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *PostgresStore) ClaimEmailDelivery(ctx context.Context, owner string) (EmailDelivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EmailDelivery{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// A revoked in-flight token may finish once; an expired token cannot be renewed automatically.
	if _, err = tx.ExecContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS now)
 UPDATE email_test_deliveries SET state='dead',error_code=automatic_cancellation_code,possible_duplicate=possible_duplicate OR state='delivering',lease_owner='',lease_token=NULL,lease_expires_at=NULL,version=version+1,updated_at=clock.now FROM clock WHERE automatic_cancellation_code<>'' AND ((state='delivering' AND lease_expires_at<=clock.now) OR state IN ('pending','retrying'))`); err != nil {
		return EmailDelivery{}, err
	}
	// Expired attempts may already have been accepted before a process crash.
	if _, err = tx.ExecContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS now)
 UPDATE email_test_deliveries SET state='dead',error_code='attempts_exhausted',possible_duplicate=possible_duplicate OR state='delivering',lease_owner='',lease_token=NULL,lease_expires_at=NULL,version=version+1,updated_at=clock.now FROM clock
 WHERE attempts>=8 AND ((state='delivering' AND lease_expires_at<=clock.now) OR (state IN ('pending','retrying') AND next_attempt_at<=clock.now))`); err != nil {
		return EmailDelivery{}, err
	}
	v, err := scanEmailDelivery(tx.QueryRowContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS now), candidate AS (
 SELECT d.id AS candidate_id FROM email_test_deliveries d,clock WHERE d.attempts<8 AND ((d.state IN ('pending','retrying') AND d.next_attempt_at<=clock.now) OR (d.state='delivering' AND d.lease_expires_at<=clock.now))
 AND d.automatic_cancellation_code=''
 AND (d.kind='test' OR NOT EXISTS (SELECT 1 FROM email_test_deliveries prior WHERE prior.quota_rule_id=d.quota_rule_id AND prior.event_sequence<d.event_sequence AND prior.state IN ('pending','retrying','delivering')))
 ORDER BY d.next_attempt_at,d.id FOR UPDATE OF d SKIP LOCKED LIMIT 1)
 UPDATE email_test_deliveries SET possible_duplicate=possible_duplicate OR state='delivering',state='delivering',attempts=attempts+1,lease_owner=$1,lease_token=$2,lease_expires_at=clock.now+interval '30 seconds',version=version+1,updated_at=clock.now FROM candidate,clock WHERE email_test_deliveries.id=candidate.candidate_id RETURNING `+emailDeliveryColumns, owner, uuid.NewString()))
	if errors.Is(err, ErrNotFound) {
		if e := tx.Commit(); e != nil {
			return EmailDelivery{}, e
		}
		return EmailDelivery{}, ErrNotFound
	}
	if err != nil {
		return EmailDelivery{}, err
	}
	return v, tx.Commit()
}
func (s *PostgresStore) FinishEmailDelivery(ctx context.Context, id, token string, result EmailAttemptResult) error {
	result.Code = emailSafeErrorCode(result.Code)
	done, err := s.db.ExecContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS now)
 UPDATE email_test_deliveries SET state=CASE WHEN $3='' THEN 'accepted' WHEN automatic_cancellation_code<>'' OR $4 OR attempts>=8 THEN 'dead' ELSE 'retrying' END,error_code=CASE WHEN $3<>'' AND automatic_cancellation_code<>'' THEN automatic_cancellation_code ELSE $3 END,possible_duplicate=possible_duplicate OR $5,accepted_at=CASE WHEN $3='' THEN clock.now ELSE accepted_at END,
 next_attempt_at=clock.now+LEAST(3600,5*power(2,GREATEST(0,attempts-1))) * interval '1 second',lease_owner='',lease_token=NULL,lease_expires_at=NULL,updated_at=clock.now,version=version+1 FROM clock WHERE id::text=$1 AND lease_token::text=$2 AND state='delivering' AND lease_expires_at>clock.now`, id, token, result.Code, result.Permanent, result.OutcomeUnknown)
	if err != nil {
		return err
	}
	count, err := done.RowsAffected()
	if err == nil && count == 0 {
		return ErrVersionConflict
	}
	return err
}
func (s *PostgresStore) ReplayEmailDelivery(ctx context.Context, id, version string) (EmailDelivery, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EmailDelivery{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(15112,229)`); err != nil {
		return EmailDelivery{}, err
	}
	v, err := scanEmailDelivery(tx.QueryRowContext(ctx, `SELECT `+emailDeliveryColumns+` FROM email_test_deliveries WHERE id::text=$1 FOR UPDATE`, id))
	if err != nil {
		return EmailDelivery{}, err
	}
	if v.Version != version {
		return EmailDelivery{}, ErrVersionConflict
	}
	if v.State != "dead" {
		return EmailDelivery{}, ErrEmailInvalidState
	}
	target, err := scanEmailTarget(tx.QueryRowContext(ctx, `SELECT `+emailTargetColumns+` FROM email_targets WHERE id::text=$1 FOR SHARE`, v.TargetID))
	if err != nil {
		return EmailDelivery{}, err
	}
	if target.Version != v.TargetVersion {
		return EmailDelivery{}, ErrVersionConflict
	}
	if !target.Enabled {
		return EmailDelivery{}, ErrEmailTargetDisabled
	}
	if err = emailRatePostgres(ctx, tx); err != nil {
		return EmailDelivery{}, err
	}
	v, err = scanEmailDelivery(tx.QueryRowContext(ctx, `UPDATE email_test_deliveries SET automatic_cancellation_code='',state='pending',attempts=0,error_code='',lease_owner='',lease_token=NULL,lease_expires_at=NULL,next_attempt_at=clock_timestamp(),updated_at=clock_timestamp(),version=version+1 WHERE id::text=$1 RETURNING `+emailDeliveryColumns, id))
	if err != nil {
		return EmailDelivery{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO email_test_requests DEFAULT VALUES`); err != nil {
		return EmailDelivery{}, err
	}
	return v, tx.Commit()
}

var _ EmailStore = (*PostgresStore)(nil)
var _ EmailStore = (*MemoryStore)(nil)
