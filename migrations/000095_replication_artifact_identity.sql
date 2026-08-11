-- +goose Up
ALTER TABLE replication_plans
    ADD COLUMN IF NOT EXISTS coordinate TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS digest TEXT NOT NULL DEFAULT '';

ALTER TABLE replication_plans
    DROP CONSTRAINT IF EXISTS replication_plans_format_check,
    ADD CONSTRAINT replication_plans_format_check
        CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi'));

ALTER TABLE replication_plans
    DROP CONSTRAINT IF EXISTS replication_plans_digest_check,
    DROP CONSTRAINT IF EXISTS replication_plans_artifact_identity_check,
    ADD CONSTRAINT replication_plans_artifact_identity_check
        CHECK (
            (coordinate = '' AND digest = '')
            OR (coordinate <> '' AND digest ~ '^sha256:[a-f0-9]{64}$')
        );

-- +goose Down
ALTER TABLE replication_plans
    DROP CONSTRAINT IF EXISTS replication_plans_digest_check,
    DROP CONSTRAINT IF EXISTS replication_plans_artifact_identity_check,
    DROP COLUMN IF EXISTS digest,
    DROP COLUMN IF EXISTS coordinate;

ALTER TABLE replication_plans
    DROP CONSTRAINT IF EXISTS replication_plans_format_check,
    ADD CONSTRAINT replication_plans_format_check
        CHECK (format IN ('raw', 'oci', 'maven', 'conan'));
