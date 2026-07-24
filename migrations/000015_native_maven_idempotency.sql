-- +goose Up
CREATE TABLE IF NOT EXISTS native_maven_publish_idempotency (
    actor TEXT NOT NULL,
    target TEXT NOT NULL,
    key TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128),
    payload_hash TEXT NOT NULL,
    session_id UUID NOT NULL REFERENCES native_maven_publish_sessions(id),
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor, target, key)
);
CREATE INDEX IF NOT EXISTS native_maven_publish_idempotency_expires_at_idx
    ON native_maven_publish_idempotency(expires_at);
