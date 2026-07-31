-- +goose Up
CREATE TABLE IF NOT EXISTS hosted_groups (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'),
    format TEXT NOT NULL CHECK (format IN ('raw', 'oci', 'maven')),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hosted_group_members (
    group_id UUID NOT NULL REFERENCES hosted_groups(id) ON DELETE CASCADE,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    position INTEGER NOT NULL CHECK (position >= 0),
    PRIMARY KEY (group_id, position),
    UNIQUE (group_id, repository_id)
);

CREATE TABLE IF NOT EXISTS hosted_group_idempotency (
    actor TEXT NOT NULL,
    key TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128),
    payload_hash TEXT NOT NULL,
    group_id UUID NOT NULL REFERENCES hosted_groups(id),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor, key)
);

-- +goose Down
-- Management aggregates are forward-only; compensate with a later migration.
