-- +goose Up
ALTER TABLE native_maven_object_intents
    ADD COLUMN IF NOT EXISTS claimed_token TEXT;
