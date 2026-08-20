-- +goose Up
ALTER TABLE replication_plans
    DROP CONSTRAINT IF EXISTS replication_plans_format_check,
    ADD CONSTRAINT replication_plans_format_check
        CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go'));

-- +goose Down
ALTER TABLE replication_plans
    DROP CONSTRAINT IF EXISTS replication_plans_format_check,
    ADD CONSTRAINT replication_plans_format_check
        CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi'));
