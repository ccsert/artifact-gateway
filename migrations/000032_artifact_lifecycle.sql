-- +goose Up
CREATE TABLE IF NOT EXISTS artifact_tombstones (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    format TEXT NOT NULL CHECK (format IN ('raw', 'oci', 'maven', 'conan')),
    coordinate TEXT NOT NULL,
    digest TEXT NOT NULL DEFAULT '',
    tombstoned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, format, coordinate)
);
CREATE INDEX IF NOT EXISTS artifact_tombstones_tombstoned_at_idx ON artifact_tombstones (tombstoned_at);

CREATE TABLE IF NOT EXISTS lifecycle_jobs (
    id UUID PRIMARY KEY,
    repository_id UUID REFERENCES hosted_repositories(id),
    kind TEXT NOT NULL CHECK (kind IN ('retention', 'promotion', 'replication', 'reclaim')),
    idempotency_key TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending', 'running', 'completed', 'failed')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    UNIQUE (repository_id, kind, idempotency_key)
);
CREATE INDEX IF NOT EXISTS lifecycle_jobs_pending_idx ON lifecycle_jobs (created_at) WHERE state = 'pending';
