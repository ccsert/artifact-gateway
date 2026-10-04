package repository

import (
	"context"
	"database/sql"
	"time"
)

func (s *MemoryStore) cancelQuotaDeliveriesLocked(id, code string, now time.Time) {
	for key, v := range s.emailDeliveries {
		if v.QuotaRuleID == id && (v.State == "pending" || v.State == "retrying" || v.State == "delivering") {
			v.AutomaticCancellationCode = code
			if v.State != "delivering" {
				v.State = "dead"
				v.ErrorCode = code
			}
			v.Version = nextHostedGroupVersion(v.Version)
			v.UpdatedAt = now
			s.emailDeliveries[key] = v
		}
	}
}
func cancelQuotaDeliveries(ctx context.Context, tx *sql.Tx, id, code string) error {
	_, err := tx.ExecContext(ctx, `UPDATE email_test_deliveries SET automatic_cancellation_code=$2,state=CASE WHEN state='delivering' THEN state ELSE 'dead' END,error_code=CASE WHEN state='delivering' THEN error_code ELSE $2 END,version=version+1,updated_at=clock_timestamp() WHERE quota_rule_id::text=$1 AND state IN ('pending','retrying','delivering')`, id, code)
	return err
}
