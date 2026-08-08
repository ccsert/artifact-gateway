-- +goose Up
CREATE TABLE IF NOT EXISTS runtime_nodes (
    instance_id TEXT PRIMARY KEY,
    roles JSONB NOT NULL DEFAULT '[]'::jsonb,
    worker_formats JSONB NOT NULL DEFAULT '[]'::jsonb,
    worker_kinds JSONB NOT NULL DEFAULT '[]'::jsonb,
    started_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS runtime_nodes_last_seen_idx
    ON runtime_nodes (last_seen_at DESC);

-- +goose Down
-- Forward-only: retain node inventory for operational history and rollbacks.
