-- +goose Up
ALTER TABLE native_raw_objects
    ADD COLUMN IF NOT EXISTS repository_id UUID REFERENCES hosted_repositories(id);

CREATE INDEX IF NOT EXISTS native_raw_objects_reclaimable_idx
    ON native_raw_objects (repository_id, created_at)
    WHERE repository_id IS NOT NULL AND collected_at IS NULL;
