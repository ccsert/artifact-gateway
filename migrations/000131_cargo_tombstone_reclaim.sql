-- +goose Up
ALTER TABLE artifact_tombstones DROP CONSTRAINT IF EXISTS artifact_tombstones_format_check;
ALTER TABLE artifact_tombstones ADD CONSTRAINT artifact_tombstones_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go', 'apt', 'cargo'));

ALTER TABLE native_cargo_publications
    ADD COLUMN collecting_at TIMESTAMPTZ,
    ADD COLUMN collected_at TIMESTAMPTZ,
    ADD CONSTRAINT native_cargo_publications_collection_check
        CHECK (collecting_at IS NULL OR collected_at IS NULL);
CREATE INDEX native_cargo_publications_reclaim_idx
    ON native_cargo_publications (object_key) WHERE collected_at IS NULL;

CREATE OR REPLACE FUNCTION assert_cargo_repository_capacity() RETURNS TRIGGER AS $$
DECLARE
    quota BIGINT;
    used BIGINT;
BEGIN
    SELECT quota_bytes INTO quota FROM repository_capacity_quotas
        WHERE repository_id=NEW.repository_id FOR UPDATE;
    IF quota IS NULL OR quota = 0 THEN RETURN NEW; END IF;
    SELECT COALESCE(SUM(size), 0) INTO used FROM native_cargo_publications
        WHERE repository_id=NEW.repository_id AND collected_at IS NULL;
    IF used > quota THEN
        RAISE EXCEPTION 'repository capacity quota exceeded' USING ERRCODE='P0001';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- +goose Down
-- Forward-only lifecycle state; collected bytes and tombstones cannot be reconstructed by a downgrade.
