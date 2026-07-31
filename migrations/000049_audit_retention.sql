-- +goose Up
CREATE TABLE IF NOT EXISTS audit_retention_policy (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    version BIGINT NOT NULL DEFAULT 1,
    enabled BOOLEAN NOT NULL DEFAULT false,
    keep_days INTEGER NOT NULL DEFAULT 0 CHECK (keep_days >= 0)
);
INSERT INTO audit_retention_policy (singleton) VALUES (true) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS audit_cleanup_jobs (
    id UUID PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    policy_version BIGINT NOT NULL,
    cutoff_at TIMESTAMPTZ NOT NULL,
    batch_size INTEGER NOT NULL CHECK (batch_size BETWEEN 1 AND 1000),
    deleted INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'completed', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS audit_cleanup_jobs_claim_idx ON audit_cleanup_jobs (created_at) WHERE state IN ('pending', 'failed');
CREATE INDEX IF NOT EXISTS resolver_audit_log_occurred_at_idx ON resolver_audit_log (occurred_at);
