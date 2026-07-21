-- +goose Up
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS request_id TEXT;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS trace_id TEXT;

-- +goose Down
-- Correlation fields are additive for compatibility.
