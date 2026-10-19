-- +goose Up
CREATE TABLE nexus_migration_sources (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    base_url TEXT NOT NULL CHECK (base_url LIKE 'https://%' AND length(base_url) <= 2048),
    credential_ciphertext TEXT NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
DROP TABLE nexus_migration_sources;
