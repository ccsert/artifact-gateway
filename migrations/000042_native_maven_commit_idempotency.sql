-- +goose Up
CREATE TABLE native_maven_commit_idempotency (
    session_id UUID PRIMARY KEY REFERENCES native_maven_publish_sessions(id),
    key TEXT NOT NULL CHECK (char_length(key) BETWEEN 1 AND 128),
    payload_hash TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);
