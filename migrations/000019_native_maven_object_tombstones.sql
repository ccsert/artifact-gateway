-- +goose Up
ALTER TABLE native_maven_object_intents
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS native_maven_object_intents_deleted_at_idx
    ON native_maven_object_intents(deleted_at) WHERE deleted_at IS NOT NULL;
