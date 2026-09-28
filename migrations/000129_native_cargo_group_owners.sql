-- +goose Up
ALTER TABLE hosted_groups DROP CONSTRAINT IF EXISTS hosted_groups_format_check;
ALTER TABLE hosted_groups ADD CONSTRAINT hosted_groups_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go', 'apt', 'cargo'));

-- Source ID is intentionally retained after a member is removed. The next
-- read must fail closed instead of silently rebinding an exposed version.
CREATE TABLE native_cargo_group_versions (
    group_id UUID NOT NULL REFERENCES hosted_groups(id) ON DELETE CASCADE,
    collision_key TEXT NOT NULL,
    version_key TEXT NOT NULL,
    source_repository_id UUID NOT NULL,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    checksum TEXT NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
    index_row BYTEA NOT NULL CHECK (octet_length(index_row) > 0),
    PRIMARY KEY (group_id, collision_key, version_key)
);

-- +goose Down
-- Cargo Group ownership is forward-only; compensate with a later migration.
