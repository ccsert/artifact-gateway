-- +goose Up
ALTER TABLE oci_group_members ADD COLUMN IF NOT EXISTS allowed_hosts TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE maven_group_members ADD COLUMN IF NOT EXISTS allowed_hosts TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE conan_group_members ADD COLUMN IF NOT EXISTS allowed_hosts TEXT[] NOT NULL DEFAULT '{}';

-- +goose Down
-- Allowlist rollback is application-first; columns remain for compatibility.
