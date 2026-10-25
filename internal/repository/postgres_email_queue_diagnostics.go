package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// One read-only statement gives every aggregate the same MVCC snapshot and clock.
// Project only the deterministic cleanup that ClaimEmailDelivery runs before its
// candidate SELECT. Do not take locks, and do not treat updated_at as activity.
func (s *PostgresStore) EmailQueueDiagnostics(ctx context.Context) (EmailQueueDiagnostics, error) {
	out := newEmailQueueDiagnostics("database", time.Time{})
	var age sql.NullFloat64
	var accepted sql.NullTime
	var failures []byte
	c := &out.Counts
	err := s.db.QueryRowContext(ctx, `WITH clock AS MATERIALIZED (SELECT clock_timestamp() AS now),
 base AS MATERIALIZED (
 SELECT d.id,d.kind,d.quota_rule_id,d.event_sequence,d.state,d.attempts,d.error_code,d.accepted_at,d.next_attempt_at,d.lease_expires_at,d.automatic_cancellation_code,
 ((d.state IN ('pending','retrying') AND d.next_attempt_at<=clock.now) OR (d.state='delivering' AND d.lease_expires_at<=clock.now)) AS due,
 (d.automatic_cancellation_code<>'' AND (d.state IN ('pending','retrying') OR (d.state='delivering' AND d.lease_expires_at<=clock.now))) AS cancelling,
 (d.attempts>=8 AND ((d.state IN ('pending','retrying') AND d.next_attempt_at<=clock.now) OR (d.state='delivering' AND d.lease_expires_at<=clock.now))) AS exhausting
 FROM email_test_deliveries d CROSS JOIN clock),
 predecessors AS (
 SELECT quota_rule_id,min(event_sequence) AS first_sequence FROM base
 WHERE state IN ('pending','retrying','delivering') AND NOT COALESCE(cancelling,false) AND NOT COALESCE(exhausting,false)
 GROUP BY quota_rule_id),
 classified AS MATERIALIZED (
 SELECT b.*, (b.attempts<8 AND b.automatic_cancellation_code='' AND b.due) AS eligible,
 (b.kind<>'test' AND COALESCE(p.first_sequence<b.event_sequence,false)) AS blocked
 FROM base b LEFT JOIN predecessors p ON p.quota_rule_id=b.quota_rule_id),
 failure_counts AS (
 SELECT CASE
 WHEN error_code IN ('email_disabled','invalid_message','relay_resolution_failed','relay_address_denied','tls_configuration_failed','tls_verification_failed','tls_required','authentication_configuration_failed','authentication_failed','encryption_key_unavailable') THEN 'configuration'
 WHEN error_code IN ('relay_connect_failed','smtp_transport_failed') THEN 'transport'
 WHEN error_code='smtp_temporary_rejection' THEN 'temporary_rejection'
 WHEN error_code='smtp_permanent_rejection' THEN 'permanent_rejection'
 WHEN error_code='outcome_unknown' THEN 'outcome_unknown'
 WHEN error_code IN ('target_changed','target_disabled','rule_changed','rule_disabled','rule_deleted') THEN 'cancelled'
 WHEN error_code='attempts_exhausted' THEN 'attempts_exhausted'
 ELSE 'unknown' END AS category,count(*) AS count FROM base WHERE error_code<>'' GROUP BY 1)
 SELECT (SELECT now FROM clock),count(*),
 count(*) FILTER (WHERE state='pending'),count(*) FILTER (WHERE state='retrying'),count(*) FILTER (WHERE state='delivering'),count(*) FILTER (WHERE state='accepted'),count(*) FILTER (WHERE state='dead'),
 count(*) FILTER (WHERE eligible AND NOT blocked),
 count(*) FILTER (WHERE state IN ('pending','retrying') AND next_attempt_at>(SELECT now FROM clock)),
 count(*) FILTER (WHERE state='delivering' AND lease_expires_at>(SELECT now FROM clock)),
 count(*) FILTER (WHERE state='delivering' AND lease_expires_at<=(SELECT now FROM clock)),
 count(*) FILTER (WHERE eligible AND blocked),count(*) FILTER (WHERE cancelling),count(*) FILTER (WHERE exhausting),
 max(GREATEST(0,EXTRACT(EPOCH FROM ((SELECT now FROM clock)-CASE WHEN state='delivering' THEN lease_expires_at ELSE next_attempt_at END)))) FILTER (WHERE eligible AND NOT blocked),
 max(accepted_at),
 (SELECT COALESCE(jsonb_agg(jsonb_build_object('category',category,'count',count) ORDER BY category),'[]'::jsonb) FROM failure_counts)
 FROM classified`).Scan(&out.SampledAt, &c.Total, &c.Pending, &c.Retrying, &c.Delivering, &c.Accepted, &c.Dead, &c.ExecutableCandidates, &c.FutureBackoff, &c.ValidLeases, &c.ExpiredLeases, &c.PredecessorBlocked, &c.AwaitingCancellation, &c.AwaitingExhaustion, &age, &accepted, &failures)
	if err != nil {
		return EmailQueueDiagnostics{}, err
	}
	if age.Valid {
		out.OldestExecutableWaitSeconds = &age.Float64
	}
	if accepted.Valid {
		out.LastSMTPAcceptedAt = &accepted.Time
	}
	if err = json.Unmarshal(failures, &out.FailureClasses); err != nil {
		return EmailQueueDiagnostics{}, err
	}
	return out, nil
}
