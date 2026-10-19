-- +goose Up
ALTER TABLE native_npm_packages
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);

ALTER TABLE native_npm_versions
    ADD COLUMN deprecated TEXT NOT NULL DEFAULT '',
    ADD COLUMN collecting_at TIMESTAMPTZ;

-- Preserve existing protocol-native deprecations without touching a tarball,
-- immutable manifest, publication timestamp, or identity reservation.
UPDATE native_npm_versions
SET deprecated=manifest->>'deprecated'
WHERE jsonb_typeof(manifest->'deprecated')='string';

-- +goose Down
-- npm metadata governance is forward-only; compensate with a later migration.
