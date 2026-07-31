-- +goose Up
ALTER TABLE native_oci_manifests ADD COLUMN IF NOT EXISTS subject_digest TEXT;
ALTER TABLE native_oci_manifests ADD COLUMN IF NOT EXISTS artifact_type TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS native_oci_manifests_referrers_idx ON native_oci_manifests (repository_id,name,subject_digest,digest) WHERE subject_digest IS NOT NULL;
