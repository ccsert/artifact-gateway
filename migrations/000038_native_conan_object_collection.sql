-- +goose Up
ALTER TABLE native_conan_object_intents ADD COLUMN IF NOT EXISTS collected_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS native_conan_object_intents_collect_idx ON native_conan_object_intents (created_at) WHERE collected_at IS NULL;
