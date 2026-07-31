-- +goose Up
CREATE TABLE IF NOT EXISTS native_oci_object_intents (
    object_key TEXT PRIMARY KEY,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    size BIGINT NOT NULL CHECK (size >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    claimed_at TIMESTAMPTZ,
    collected_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS native_oci_object_intents_unclaimed_idx
    ON native_oci_object_intents (created_at)
    WHERE claimed_at IS NULL AND collected_at IS NULL;
