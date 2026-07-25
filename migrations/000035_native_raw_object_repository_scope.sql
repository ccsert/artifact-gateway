-- +goose Up
ALTER TABLE native_raw_objects
    ADD COLUMN repository_id UUID REFERENCES hosted_repositories(id);

CREATE INDEX native_raw_objects_reclaimable_idx
    ON native_raw_objects (repository_id, created_at)
    WHERE repository_id IS NOT NULL AND collected_at IS NULL;
