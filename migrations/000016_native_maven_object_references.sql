-- +goose Up
CREATE TABLE IF NOT EXISTS native_maven_object_intents (
    object_key TEXT PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES native_maven_publish_sessions(id) ON DELETE CASCADE,
    digest TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS native_maven_object_intents_collect_idx
    ON native_maven_object_intents(created_at) WHERE claimed_at IS NULL;
CREATE TABLE IF NOT EXISTS native_maven_object_references (
    object_key TEXT PRIMARY KEY REFERENCES native_maven_object_intents(object_key) ON DELETE CASCADE,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
