-- +goose Up
CREATE TABLE native_raw_objects (
    digest TEXT PRIMARY KEY CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL UNIQUE,
    size BIGINT NOT NULL CHECK (size >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    collected_at TIMESTAMPTZ
);
CREATE TABLE native_raw_assets (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    path TEXT NOT NULL,
    digest TEXT NOT NULL REFERENCES native_raw_objects(digest),
    content_type TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id,path)
);
CREATE INDEX native_raw_assets_digest_idx ON native_raw_assets (digest);
