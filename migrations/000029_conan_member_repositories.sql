-- +goose Up
ALTER TABLE hosted_repositories DROP CONSTRAINT IF EXISTS hosted_repositories_format_check;
ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_format_check CHECK (format IN ('raw', 'oci', 'maven', 'conan'));

ALTER TABLE conan_group_members ADD COLUMN IF NOT EXISTS repository_id UUID REFERENCES hosted_repositories(id);
CREATE INDEX IF NOT EXISTS conan_group_members_repository_id_idx ON conan_group_members (repository_id) WHERE repository_id IS NOT NULL;
