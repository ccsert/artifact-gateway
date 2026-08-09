-- +goose Up
ALTER TABLE hosted_groups DROP CONSTRAINT IF EXISTS hosted_groups_format_check;
ALTER TABLE hosted_groups ADD CONSTRAINT hosted_groups_format_check CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm'));

-- +goose Down
-- npm Group support is forward-only; compensate with a later migration.
