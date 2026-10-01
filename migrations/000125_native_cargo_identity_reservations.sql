-- +goose Up
-- Internal C0 reservations. These rows are not sparse-index entries and do not
-- make Cargo a public repository format.
CREATE TABLE native_cargo_names (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    collision_key TEXT NOT NULL,
    name TEXT NOT NULL,
    normalized_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, collision_key),
    CHECK (name ~ '^[A-Za-z][A-Za-z0-9_-]{0,63}$'),
    CHECK (normalized_name = lower(name)),
    CHECK (collision_key = replace(normalized_name, '_', '-'))
);

CREATE TABLE native_cargo_identity_reservations (
    repository_id UUID NOT NULL,
    collision_key TEXT NOT NULL,
    version_key TEXT NOT NULL,
    version TEXT NOT NULL,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    metadata_digest TEXT NOT NULL CHECK (metadata_digest ~ '^sha256:[0-9a-f]{64}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, collision_key, version_key),
    FOREIGN KEY (repository_id, collision_key)
        REFERENCES native_cargo_names(repository_id, collision_key) ON DELETE CASCADE,
    CHECK (length(version) BETWEEN 1 AND 256),
    CHECK (version_key = split_part(version, '+', 1))
);

-- +goose Down
DROP TABLE native_cargo_identity_reservations;
DROP TABLE native_cargo_names;
