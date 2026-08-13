-- +goose Up
ALTER TABLE native_apt_snapshot_object_intents
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE INDEX IF NOT EXISTS native_apt_snapshot_object_intents_age_idx
    ON native_apt_snapshot_object_intents (created_at, snapshot_id)
    WHERE collected_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS native_apt_snapshot_object_intents_age_idx;
ALTER TABLE native_apt_snapshot_object_intents
    DROP COLUMN IF EXISTS created_at;
