-- +goose Up
ALTER TABLE oci_groups ADD COLUMN IF NOT EXISTS anonymous BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE oci_group_members ADD COLUMN IF NOT EXISTS anonymous BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE maven_groups ADD COLUMN IF NOT EXISTS anonymous BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE maven_group_members ADD COLUMN IF NOT EXISTS anonymous BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE conan_groups ADD COLUMN IF NOT EXISTS anonymous BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE conan_group_members ADD COLUMN IF NOT EXISTS anonymous BOOLEAN NOT NULL DEFAULT false;

-- +goose Down
-- Policy rollback is application-first; columns remain for compatibility.
