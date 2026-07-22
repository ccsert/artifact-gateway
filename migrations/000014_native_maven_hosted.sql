-- +goose Up
CREATE TABLE IF NOT EXISTS native_maven_publish_sessions (
    id UUID PRIMARY KEY, repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    coordinate TEXT NOT NULL, pom_object TEXT NOT NULL, state TEXT NOT NULL CHECK (state IN ('open','committed','aborted','expired')),
    expires_at TIMESTAMPTZ NOT NULL, objects JSONB NOT NULL
);
CREATE TABLE IF NOT EXISTS native_maven_publish_uploads (
    session_id UUID NOT NULL REFERENCES native_maven_publish_sessions(id) ON DELETE CASCADE,
    object_name TEXT NOT NULL, object_key TEXT NOT NULL, PRIMARY KEY (session_id, object_name)
);
CREATE TABLE IF NOT EXISTS native_maven_assets (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id), path TEXT NOT NULL,
    object_key TEXT NOT NULL, digest TEXT NOT NULL, size BIGINT NOT NULL CHECK (size >= 0), PRIMARY KEY (repository_id,path)
);
CREATE TABLE IF NOT EXISTS native_maven_artifacts (
    id UUID PRIMARY KEY, repository_id UUID NOT NULL REFERENCES hosted_repositories(id), coordinate TEXT NOT NULL,
    digest TEXT NOT NULL, state TEXT NOT NULL CHECK (state IN ('visible','deleted')), created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(repository_id,coordinate)
);
