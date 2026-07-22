-- +goose Up
CREATE TABLE IF NOT EXISTS hosted_repository_idempotency (
    actor TEXT NOT NULL,
    target TEXT NOT NULL,
    key TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128),
    payload_hash TEXT NOT NULL,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor, target, key)
);
CREATE INDEX IF NOT EXISTS hosted_repository_idempotency_expires_at_idx ON hosted_repository_idempotency (expires_at);
