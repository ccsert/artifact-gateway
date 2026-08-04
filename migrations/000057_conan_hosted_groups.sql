-- +goose Up
ALTER TABLE hosted_groups DROP CONSTRAINT IF EXISTS hosted_groups_format_check;
ALTER TABLE hosted_groups ADD CONSTRAINT hosted_groups_format_check CHECK (format IN ('raw', 'oci', 'maven', 'conan'));

-- +goose Down
-- The Conan format is part of the V2 hosted group contract; retain the widened constraint.
