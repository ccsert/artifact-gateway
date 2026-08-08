-- +goose Up
CREATE TABLE IF NOT EXISTS runtime_node_sessions (
    session_id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    roles JSONB NOT NULL DEFAULT '[]'::jsonb,
    worker_formats JSONB NOT NULL DEFAULT '[]'::jsonb,
    worker_kinds JSONB NOT NULL DEFAULT '[]'::jsonb,
    started_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    stopped_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS runtime_node_sessions_instance_idx
    ON runtime_node_sessions (instance_id, last_seen_at DESC);

CREATE INDEX IF NOT EXISTS runtime_node_sessions_last_seen_idx
    ON runtime_node_sessions (last_seen_at DESC);

-- +goose Down
-- Forward-only: retain node session history for operational diagnosis.
