-- +goose Up
CREATE TABLE IF NOT EXISTS hosted_repositories (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE CHECK (name ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'),
    format TEXT NOT NULL CHECK (format IN ('raw', 'oci', 'maven')),
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'deleting', 'deleted')),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hosted_repositories_created_at_idx ON hosted_repositories (created_at, id);
