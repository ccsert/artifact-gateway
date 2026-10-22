-- +goose Up
-- Retain publication identities, objects and capacity accounting during
-- unlisting, tombstoning and quarantine. NuGet remains an explicit preview.
ALTER TABLE native_nuget_publications
 ADD COLUMN governance_version BIGINT NOT NULL DEFAULT 1 CHECK(governance_version > 0),
 ADD COLUMN listed BOOLEAN NOT NULL DEFAULT true,
 ADD COLUMN tombstoned BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN quarantined BOOLEAN NOT NULL DEFAULT false,
 ADD COLUMN governance_updated_at TIMESTAMPTZ,
 ADD COLUMN governance_updated_by TEXT NOT NULL DEFAULT '' CHECK(length(governance_updated_by) <= 512),
 ADD COLUMN governance_reason TEXT NOT NULL DEFAULT '' CHECK(length(governance_reason) <= 2048);

UPDATE native_nuget_publications
 SET listed=COALESCE((metadata_json::jsonb->>'listed')::boolean,true);

-- +goose Down
-- Forward-only: older binaries do not understand NuGet governance barriers.
