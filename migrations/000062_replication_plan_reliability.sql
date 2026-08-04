-- +goose Up
ALTER TABLE replication_plans
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ DEFAULT now(),
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS lease_token TEXT,
    ADD COLUMN IF NOT EXISTS attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS max_attempts INTEGER NOT NULL DEFAULT 3;

ALTER TABLE replication_plans
    ALTER COLUMN next_attempt_at DROP NOT NULL;

UPDATE replication_plans
SET next_attempt_at = COALESCE(next_attempt_at, created_at),
    max_attempts = CASE WHEN max_attempts < 1 THEN 3 ELSE max_attempts END
WHERE next_attempt_at IS NULL OR max_attempts < 1;

ALTER TABLE replication_plans
    DROP CONSTRAINT IF EXISTS replication_plans_state_check,
    DROP CONSTRAINT IF EXISTS replication_plans_attempts_check,
    DROP CONSTRAINT IF EXISTS replication_plans_max_attempts_check,
    ADD CONSTRAINT replication_plans_state_check CHECK (state IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    ADD CONSTRAINT replication_plans_attempts_check CHECK (attempts >= 0),
    ADD CONSTRAINT replication_plans_max_attempts_check CHECK (max_attempts BETWEEN 1 AND 10);

CREATE INDEX IF NOT EXISTS replication_plans_retry_idx
    ON replication_plans (next_attempt_at, created_at)
    WHERE state IN ('pending', 'failed');

-- +goose Down
-- Forward-only: application rollback keeps these additive reliability columns.
