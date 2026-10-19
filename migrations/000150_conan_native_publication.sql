-- +goose Up
ALTER TABLE native_conan_publish_sessions
    ADD COLUMN native BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN version BIGINT NOT NULL DEFAULT 0 CHECK (version >= 0);

CREATE UNIQUE INDEX native_conan_open_publication_identity
    ON native_conan_publish_sessions
    (repository_id, publisher, kind, reference, recipe_revision, package_id, package_revision)
    WHERE native AND state='open';

-- Native uploads reuse the existing session/upload and object-intent records.
-- Expired sessions keep their evidence but cannot be resumed or published.
