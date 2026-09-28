-- +goose Up
ALTER TABLE replication_plans DROP CONSTRAINT IF EXISTS replication_plans_format_check;
ALTER TABLE replication_plans ADD CONSTRAINT replication_plans_format_check
    CHECK (format IN ('maven', 'oci', 'raw', 'conan', 'npm', 'pypi', 'go', 'apt', 'cargo'));

-- +goose Down
-- Forward-only replication state; live Cargo plans cannot be discarded by a downgrade.
