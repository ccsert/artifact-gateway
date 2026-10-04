package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"strings"
	"time"
)

func (s *PostgresStore) EvaluateNextRepositoryQuotaAlert(ctx context.Context, cfg QuotaAlertMailConfig, interval time.Duration) (bool, error) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	rule, err := scanQuotaRule(tx.QueryRowContext(ctx, `SELECT `+quotaRuleColumns+` FROM repository_quota_alert_rules WHERE enabled AND NOT deleted AND next_evaluate_at<=clock_timestamp() ORDER BY next_evaluate_at,id FOR UPDATE SKIP LOCKED LIMIT 1`))
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// A single SQL statement captures logical usage, quota and repository state.
	var record RepositoryCapacityRecord
	query := `SELECT capacity.*,statement_timestamp() FROM (` + strings.TrimSuffix(repositoryCapacityRecordsQuery, "ORDER BY h.id") + `WHERE h.id::text=$1) capacity`
	var sampledAt time.Time
	err = tx.QueryRowContext(ctx, query, rule.RepositoryID).Scan(&record.Repository.ID, &record.Repository.Name, &record.Repository.Format, &record.Repository.Type, &record.Repository.Endpoint, &record.Repository.State, &record.Capacity.QuotaBytes, &record.Capacity.UsedBytes, &record.Capacity.ObjectCount, &sampledAt)
	o := quotaalert.Observation{DataState: "repository_deleted"}
	if err == nil {
		o.DataState = "repository_inactive"
		if record.Repository.State == RepositoryDeleted {
			o.DataState = "repository_deleted"
		}
	}
	if err == nil && record.Repository.State == RepositoryActive {
		o.DataState = "available"
		o.UsedBytes, o.QuotaBytes = record.Capacity.UsedBytes, record.Capacity.QuotaBytes
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		// Fence a best-effort unknown state against a newer evaluation/configuration.
		reset, _ := json.Marshal(quotaalert.Reset(rule.State, "unknown"))
		_, _ = s.db.ExecContext(ctx, `UPDATE repository_quota_alert_rules SET state=$4,state_version=state_version+1,evaluated_at=clock_timestamp(),next_evaluate_at=clock_timestamp()+$5::bigint*interval '1 millisecond' WHERE id::text=$1 AND version::text=$2 AND state_version::text=$3`, rule.ID, rule.Version, rule.StateVersion, reset, interval.Milliseconds())
		return true, err
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return true, err
	}
	o.SampleAt = sampledAt
	if o.SampleAt.IsZero() {
		o.SampleAt = now
	}
	event := quotaTransition(&rule, o, record.Repository.Name, now)
	if event != nil {
		target, e := scanEmailTarget(tx.QueryRowContext(ctx, `SELECT `+emailTargetColumns+` FROM email_targets WHERE id::text=$1 FOR SHARE`, rule.TargetID))
		if e != nil && !errors.Is(e, ErrNotFound) {
			return true, e
		}
		code := quotaRouteCode(cfg, target, rule)
		var deliveryID any
		snapshot, _ := json.Marshal(event)
		if code == "queued" {
			// Same queue cap lock as test submission/replay. Count after acquiring it.
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(15112,229)`); err != nil {
				return true, err
			}
			var active int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM email_test_deliveries WHERE state IN ('pending','retrying','delivering')`).Scan(&active); err != nil {
				return true, err
			}
			d := quotaDelivery(*event, target, cfg, active >= 1000)
			if active >= 1000 {
				code = "queue_full"
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO email_test_deliveries(id,event_id,request_key,target_id,target_version,scenario,locale,template_version,recipient_ciphertext,from_address,console_origin,state,error_code,kind,quota_rule_id,episode_id,event_sequence,quota_descriptor,created_at,updated_at,next_attempt_at) VALUES($1,$2,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'repository_quota',$13,$14,$15,$16,$17,$17,$17)`, d.ID, d.EventID, d.TargetID, d.TargetVersion, d.Scenario, d.Locale, d.TemplateVersion, d.RecipientCiphertext, d.From, d.ConsoleOrigin, d.State, d.ErrorCode, d.QuotaRuleID, d.EpisodeID, d.EventSequence, snapshot, now)
			if err != nil {
				return true, err
			}
			deliveryID = d.ID
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO repository_quota_alert_events(id,rule_id,rule_version,episode_id,sequence,scenario,snapshot,target_id,target_version,delivery_id,notification_code) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, event.ID, rule.ID, rule.Version, event.EpisodeID, event.Sequence, event.Scenario, snapshot, rule.TargetID, rule.TargetVersion, deliveryID, code)
		if err != nil {
			return true, err
		}
	}
	state, _ := json.Marshal(rule.State)
	_, err = tx.ExecContext(ctx, `UPDATE repository_quota_alert_rules SET state=$2,state_version=state_version+1,active_episode_id=NULLIF($3,'')::uuid,last_event_id=NULLIF($4,'')::uuid,sequence=$5,evaluated_at=$6::timestamptz,next_evaluate_at=$6::timestamptz+$7::bigint*interval '1 millisecond' WHERE id::text=$1`, rule.ID, state, rule.ActiveEpisodeID, rule.LastEventID, rule.Sequence, now, interval.Milliseconds())
	if err != nil {
		return true, err
	}
	return true, tx.Commit()
}
