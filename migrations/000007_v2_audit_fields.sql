-- +goose Up
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS format TEXT;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS resource TEXT;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS representation TEXT;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS member_type TEXT;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS upstream_host TEXT;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS operation TEXT;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS http_status INTEGER;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS cache_disposition TEXT;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS bytes BIGINT;

-- +goose Down
-- V2 audit fields are additive for compatibility.
