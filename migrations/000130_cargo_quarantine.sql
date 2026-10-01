-- +goose Up
-- Internal Cargo Hosted publications can be governed before public format admission.
ALTER TABLE artifact_quarantines DROP CONSTRAINT IF EXISTS artifact_quarantines_format_check;
ALTER TABLE artifact_quarantines ADD CONSTRAINT artifact_quarantines_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go', 'apt', 'cargo'));

-- +goose Down
-- Forward-only format admission; keep persisted governance records recoverable.
