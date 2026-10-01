-- +goose Up
-- Sparse metadata and verified archives are separate visibility boundaries.
-- The index body is preserved byte-for-byte, including future Cargo fields.
CREATE TABLE native_cargo_proxy_configs (
    repository_id UUID PRIMARY KEY REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    download_template TEXT NOT NULL,
    search_api TEXT NOT NULL DEFAULT '',
    fetched_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE native_cargo_proxy_indexes (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    collision_key TEXT NOT NULL,
    name TEXT NOT NULL,
    body BYTEA NOT NULL DEFAULT ''::bytea,
    status INTEGER NOT NULL CHECK (status IN (200, 404, 410, 451)),
    upstream_etag TEXT NOT NULL DEFAULT '',
    upstream_modified TEXT NOT NULL DEFAULT '',
    fetched_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (repository_id, collision_key),
    CHECK ((status = 200 AND octet_length(body) > 0) OR (status <> 200 AND octet_length(body) = 0))
);

CREATE TABLE native_cargo_proxy_crates (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    collision_key TEXT NOT NULL,
    version_key TEXT NOT NULL,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    checksum TEXT NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
    object_key TEXT NOT NULL CHECK (object_key ~ '^native/cargo-proxy/sha256/[0-9a-f]{64}$'),
    size BIGINT NOT NULL CHECK (size > 0),
    cached_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (repository_id, collision_key, version_key),
    FOREIGN KEY (repository_id, collision_key) REFERENCES native_cargo_proxy_indexes(repository_id, collision_key)
);

-- +goose Down
-- Cargo proxy cache state is forward-only; compensate with a later migration.
