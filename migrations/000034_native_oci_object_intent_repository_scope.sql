-- +goose Up
ALTER TABLE native_oci_object_intents
    ADD COLUMN IF NOT EXISTS repository_id UUID REFERENCES hosted_repositories(id);

CREATE INDEX IF NOT EXISTS native_oci_object_intents_reclaimable_idx
    ON native_oci_object_intents (repository_id, created_at)
    WHERE repository_id IS NOT NULL AND claimed_at IS NULL AND collected_at IS NULL;
