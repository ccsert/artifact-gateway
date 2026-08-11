-- +goose Up
CREATE INDEX IF NOT EXISTS lifecycle_jobs_artifact_scan_identity_idx
    ON lifecycle_jobs (
        repository_id,
        (payload->>'format'),
        (payload->>'coordinate'),
        (payload->>'digest'),
        created_at DESC
    )
    WHERE kind = 'scan';

COMMENT ON INDEX lifecycle_jobs_artifact_scan_identity_idx IS
    'Supports authoritative latest scan status lookups by immutable artifact identity';

-- +goose Down
DROP INDEX IF EXISTS lifecycle_jobs_artifact_scan_identity_idx;
