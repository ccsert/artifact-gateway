-- +goose Up
CREATE TABLE native_raw_uploads (
    id UUID PRIMARY KEY,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    path TEXT NOT NULL,
    object_key TEXT NOT NULL UNIQUE,
    byte_offset BIGINT NOT NULL DEFAULT 0 CHECK (byte_offset >= 0),
    state TEXT NOT NULL CHECK (state IN ('open','completed','cancelled')),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX native_raw_uploads_open_idx ON native_raw_uploads (repository_id, expires_at) WHERE state='open';

-- +goose Down
-- Raw upload sessions are additive; compensate forward rather than removing them.
