-- +goose Up
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS authorization_source TEXT;
ALTER TABLE resolver_audit_log ADD COLUMN IF NOT EXISTS authorization_reason TEXT;
