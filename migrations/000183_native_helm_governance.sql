-- +goose Up
-- Overlay only: immutable publication references, retained capacity and fixed
-- Group authorities survive tombstone and quarantine transitions unchanged.
ALTER TABLE native_helm_publications
 ADD COLUMN governance_version bigint NOT NULL DEFAULT 1 CHECK(governance_version>0),
 ADD COLUMN tombstoned boolean NOT NULL DEFAULT false,
 ADD COLUMN quarantined boolean NOT NULL DEFAULT false,
 ADD COLUMN governance_updated_at timestamptz,
 ADD COLUMN governance_updated_by text NOT NULL DEFAULT '',
 ADD COLUMN governance_reason text NOT NULL DEFAULT '',
 ADD CONSTRAINT native_helm_governance_audit CHECK(
  (governance_version=1 AND governance_updated_at IS NULL AND governance_updated_by='' AND governance_reason='') OR
  (governance_version>1 AND governance_updated_at IS NOT NULL AND length(governance_updated_by)>0 AND length(btrim(governance_reason))>0)
 );

-- +goose Down
-- Forward-only: retained chart governance is not silently removed.
