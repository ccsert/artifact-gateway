package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/artifact-gateway/artifact-gateway/internal/quotaalert"
	"time"
)

func (s *PostgresStore) ListRepositoryQuotaAlertEvents(ctx context.Context, id string) ([]RepositoryQuotaAlertEvent, error) {
	if _, err := s.GetRepositoryQuotaAlertRule(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.snapshot,e.target_id::text,e.target_version::text,COALESCE(e.delivery_id::text,''),e.notification_code,COALESCE(d.state,''),COALESCE(d.error_code,'') FROM repository_quota_alert_events e LEFT JOIN email_test_deliveries d ON d.id=e.delivery_id WHERE e.rule_id::text=$1 ORDER BY e.sequence DESC LIMIT 50`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]RepositoryQuotaAlertEvent, 0)
	for rows.Next() {
		var v RepositoryQuotaAlertEvent
		var snapshot []byte
		if err = rows.Scan(&snapshot, &v.TargetID, &v.TargetVersion, &v.DeliveryID, &v.NotificationCode, &v.DeliveryState, &v.DeliveryErrorCode); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(snapshot, &v.Snapshot); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

const quotaRuleColumns = `id::text,repository_id::text,target_id::text,target_version::text,enabled,deleted,version::text,state_version::text,policy,state,COALESCE(active_episode_id::text,''),COALESCE(last_event_id::text,''),sequence,created_at,updated_at,evaluated_at,clock_timestamp()`

func scanQuotaRule(row interface{ Scan(...any) error }) (RepositoryQuotaAlertRule, error) {
	var v RepositoryQuotaAlertRule
	var policy, state []byte
	var evaluated sql.NullTime
	var now time.Time
	err := row.Scan(&v.ID, &v.RepositoryID, &v.TargetID, &v.TargetVersion, &v.Enabled, &v.Deleted, &v.Version, &v.StateVersion, &policy, &state, &v.ActiveEpisodeID, &v.LastEventID, &v.Sequence, &v.CreatedAt, &v.UpdatedAt, &evaluated, &now)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(policy, &v.Policy); err != nil {
		return v, err
	}
	if err = json.Unmarshal(state, &v.State); err != nil {
		return v, err
	}
	if evaluated.Valid {
		v.EvaluatedAt = evaluated.Time
	}
	return quotaRuleAt(v, now), nil
}

func (s *PostgresStore) GetRepositoryQuotaAlertRule(ctx context.Context, id string) (RepositoryQuotaAlertRule, error) {
	return scanQuotaRule(s.db.QueryRowContext(ctx, `SELECT `+quotaRuleColumns+` FROM repository_quota_alert_rules WHERE id::text=$1`, id))
}
func (s *PostgresStore) UpdateRepositoryQuotaAlertRule(ctx context.Context, v RepositoryQuotaAlertRule, version string) (RepositoryQuotaAlertRule, error) {
	if err := v.Policy.Validate(); err != nil {
		return v, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer func() { _ = tx.Rollback() }()
	old, err := scanQuotaRule(tx.QueryRowContext(ctx, `SELECT `+quotaRuleColumns+` FROM repository_quota_alert_rules WHERE id::text=$1 FOR UPDATE`, v.ID))
	if err != nil {
		return v, err
	}
	if old.Version != version {
		return old, ErrVersionConflict
	}
	if old.Deleted {
		return old, ErrQuotaAlertDeleted
	}
	if old.RepositoryID != v.RepositoryID {
		return old, ErrQuotaAlertScopeImmutable
	}
	target, err := scanEmailTarget(tx.QueryRowContext(ctx, `SELECT `+emailTargetColumns+` FROM email_targets WHERE id::text=$1 FOR SHARE`, v.TargetID))
	if err != nil {
		return old, err
	}
	if target.Version != v.TargetVersion {
		return old, ErrVersionConflict
	}
	if v.Enabled && !target.Enabled {
		return old, ErrEmailTargetDisabled
	}
	if quotaSameConfig(old, v) {
		return old, nil
	}
	quality := "disabled"
	if v.Enabled {
		quality = "configuration_changed"
	}
	policy, _ := json.Marshal(v.Policy)
	state, _ := json.Marshal(quotaalert.Reset(old.State, quality))
	out, err := scanQuotaRule(tx.QueryRowContext(ctx, `UPDATE repository_quota_alert_rules SET target_id=$2,target_version=$3,enabled=$4,policy=$5,state=$6,version=version+1,state_version=state_version+1,updated_at=clock_timestamp(),next_evaluate_at=clock_timestamp() WHERE id::text=$1 RETURNING `+quotaRuleColumns, v.ID, v.TargetID, v.TargetVersion, v.Enabled, policy, state))
	if err != nil {
		return old, err
	}
	code := "rule_changed"
	if !v.Enabled {
		code = "rule_disabled"
	}
	if err = cancelQuotaDeliveries(ctx, tx, v.ID, code); err != nil {
		return old, err
	}
	return out, tx.Commit()
}
func (s *PostgresStore) DeleteRepositoryQuotaAlertRule(ctx context.Context, id, version string) (RepositoryQuotaAlertRule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RepositoryQuotaAlertRule{}, err
	}
	defer func() { _ = tx.Rollback() }()
	old, err := scanQuotaRule(tx.QueryRowContext(ctx, `SELECT `+quotaRuleColumns+` FROM repository_quota_alert_rules WHERE id::text=$1 FOR UPDATE`, id))
	if err != nil {
		return old, err
	}
	if old.Version != version {
		return old, ErrVersionConflict
	}
	if old.Deleted {
		return old, nil
	}
	state, _ := json.Marshal(quotaalert.Reset(old.State, "deleted"))
	out, err := scanQuotaRule(tx.QueryRowContext(ctx, `UPDATE repository_quota_alert_rules SET enabled=false,deleted=true,state=$2,version=version+1,state_version=state_version+1,updated_at=clock_timestamp() WHERE id::text=$1 RETURNING `+quotaRuleColumns, id, state))
	if err != nil {
		return old, err
	}
	if err = cancelQuotaDeliveries(ctx, tx, id, "rule_deleted"); err != nil {
		return old, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) CreateRepositoryQuotaAlertRule(ctx context.Context, v RepositoryQuotaAlertRule) (RepositoryQuotaAlertRule, error) {
	if err := v.Policy.Validate(); err != nil {
		return v, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(15112,237)`); err != nil {
		return v, err
	}
	var repoState string
	if err = tx.QueryRowContext(ctx, `SELECT state FROM hosted_repositories WHERE id::text=$1 FOR SHARE`, v.RepositoryID).Scan(&repoState); errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	if repoState != "active" {
		return v, ErrDisabled
	}
	target, err := scanEmailTarget(tx.QueryRowContext(ctx, `SELECT `+emailTargetColumns+` FROM email_targets WHERE id::text=$1 FOR SHARE`, v.TargetID))
	if err != nil {
		return v, err
	}
	if target.Version != v.TargetVersion {
		return v, ErrVersionConflict
	}
	if v.Enabled && !target.Enabled {
		return v, ErrEmailTargetDisabled
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM repository_quota_alert_rules`).Scan(&count); err != nil {
		return v, err
	}
	if count >= 100 {
		return v, ErrQuotaAlertLimit
	}
	quality := "disabled"
	if v.Enabled {
		quality = "unknown"
	}
	policy, _ := json.Marshal(v.Policy)
	state, _ := json.Marshal(quotaalert.Reset(quotaalert.State{}, quality))
	out, err := scanQuotaRule(tx.QueryRowContext(ctx, `INSERT INTO repository_quota_alert_rules(id,repository_id,target_id,target_version,enabled,policy,state) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING `+quotaRuleColumns, v.ID, v.RepositoryID, v.TargetID, v.TargetVersion, v.Enabled, policy, state))
	if isUnique(err) {
		return v, ErrQuotaAlertConflict
	}
	if err != nil {
		return v, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) ListRepositoryQuotaAlertRules(ctx context.Context) ([]RepositoryQuotaAlertRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+quotaRuleColumns+` FROM repository_quota_alert_rules ORDER BY id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]RepositoryQuotaAlertRule, 0)
	for rows.Next() {
		v, e := scanQuotaRule(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
