-- PostgreSQL owns cross-instance cache coordination. Cache bytes remain in
-- object storage; this table contains only short-lived control-plane state.
CREATE TABLE cache_circuit_breakers (
    key TEXT PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX cache_circuit_breakers_expires_at_idx ON cache_circuit_breakers (expires_at);

-- Indexes, negative entries, and garbage-collection candidates are cache
-- control-plane records. Keeping them here makes object storage byte-only.
CREATE TABLE cache_control_entries (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX cache_control_entries_key_prefix_idx ON cache_control_entries (key text_pattern_ops);
