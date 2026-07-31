-- Durable work queue for cache maintenance. Workers claim work with
-- FOR UPDATE SKIP LOCKED so any Gateway instance can resume after another
-- instance exits or loses its database connection.
CREATE TABLE IF NOT EXISTS cache_tasks (
    id UUID PRIMARY KEY,
    task_type TEXT NOT NULL,
    dedupe_key TEXT NOT NULL,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at TIMESTAMPTZ,
    claim_token UUID,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS cache_tasks_active_dedupe_idx ON cache_tasks (dedupe_key) WHERE completed_at IS NULL;
CREATE INDEX IF NOT EXISTS cache_tasks_claim_idx ON cache_tasks (task_type, available_at, created_at) WHERE completed_at IS NULL;
