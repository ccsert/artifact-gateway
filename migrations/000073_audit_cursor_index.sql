-- +goose Up
-- Keep the descending timestamp/id order covered for audit cursor scans.
CREATE INDEX IF NOT EXISTS resolver_audit_log_occurred_at_id_idx
    ON resolver_audit_log (occurred_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS resolver_audit_log_occurred_at_id_idx;
