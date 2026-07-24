-- +goose Up
CREATE TABLE native_oci_uploads (
    id UUID PRIMARY KEY,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    name TEXT NOT NULL,
    object_key TEXT NOT NULL UNIQUE,
    byte_offset BIGINT NOT NULL DEFAULT 0 CHECK (byte_offset >= 0),
    state TEXT NOT NULL CHECK (state IN ('open','completed','expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    collected_at TIMESTAMPTZ
);
CREATE INDEX native_oci_uploads_expiry_idx ON native_oci_uploads (expires_at) WHERE state = 'open';

CREATE TABLE native_oci_blobs (
    digest TEXT PRIMARY KEY CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL UNIQUE,
    size BIGINT NOT NULL CHECK (size >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE native_oci_repository_blobs (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    digest TEXT NOT NULL REFERENCES native_oci_blobs(digest),
    PRIMARY KEY (repository_id, digest)
);
CREATE TABLE native_oci_manifests (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    name TEXT NOT NULL,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL UNIQUE,
    media_type TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, name, digest)
);
CREATE TABLE native_oci_tags (
    repository_id UUID NOT NULL,
    name TEXT NOT NULL,
    tag TEXT NOT NULL,
    digest TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, name, tag),
    FOREIGN KEY (repository_id, name, digest)
        REFERENCES native_oci_manifests (repository_id, name, digest)
);
