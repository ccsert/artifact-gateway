-- +goose Up
ALTER TABLE native_oci_manifests ADD COLUMN subject_digest TEXT;
ALTER TABLE native_oci_manifests ADD COLUMN artifact_type TEXT NOT NULL DEFAULT '';
CREATE INDEX native_oci_manifests_referrers_idx ON native_oci_manifests (repository_id,name,subject_digest,digest) WHERE subject_digest IS NOT NULL;
