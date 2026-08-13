-- +goose Up
ALTER TABLE resolver_audit_log
    ADD COLUMN IF NOT EXISTS evidence JSONB NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(evidence) = 'object');

-- +goose Down
ALTER TABLE resolver_audit_log
    DROP COLUMN IF EXISTS evidence;
