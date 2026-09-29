-- +goose Up
CREATE INDEX IF NOT EXISTS resolver_audit_log_repository_occurred_at_idx
    ON resolver_audit_log (repository, occurred_at DESC);

-- +goose Down
DROP INDEX IF EXISTS resolver_audit_log_repository_occurred_at_idx;
