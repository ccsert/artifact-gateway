-- +goose Up
CREATE TABLE IF NOT EXISTS native_conan_publish_sessions (
    id UUID PRIMARY KEY,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    publisher TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('recipe', 'package')),
    reference TEXT NOT NULL,
    recipe_revision TEXT NOT NULL,
    package_id TEXT NOT NULL DEFAULT '',
    package_revision TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL CHECK (state IN ('open', 'committed', 'aborted', 'expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    objects JSONB NOT NULL
);

CREATE TABLE IF NOT EXISTS native_conan_publish_uploads (
    session_id UUID NOT NULL REFERENCES native_conan_publish_sessions(id) ON DELETE CASCADE,
    object_name TEXT NOT NULL,
    object_key TEXT NOT NULL,
    PRIMARY KEY (session_id, object_name)
);
