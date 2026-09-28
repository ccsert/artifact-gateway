-- +goose Up
-- Cargo is a storage-only Hosted target at C1. Management format admission is
-- intentionally left to the final Cargo capability/profile migration.
ALTER TABLE hosted_repositories DROP CONSTRAINT IF EXISTS hosted_repositories_format_check;
ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go', 'apt', 'cargo'));

CREATE TABLE native_cargo_publications (
    repository_id UUID NOT NULL,
    collision_key TEXT NOT NULL,
    version_key TEXT NOT NULL,
    object_key TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size > 0),
    index_row TEXT NOT NULL CHECK (jsonb_typeof(index_row::jsonb) = 'object'),
    publisher TEXT NOT NULL CHECK (publisher <> ''),
    published_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, collision_key, version_key),
    FOREIGN KEY (repository_id, collision_key, version_key)
        REFERENCES native_cargo_identity_reservations(repository_id, collision_key, version_key) ON DELETE CASCADE,
    CHECK (object_key ~ '^native/cargo/sha256/[0-9a-f]{64}$')
);
CREATE OR REPLACE FUNCTION assert_cargo_repository_capacity() RETURNS TRIGGER AS $$
DECLARE
    quota BIGINT;
    used BIGINT;
BEGIN
    SELECT quota_bytes INTO quota FROM repository_capacity_quotas
        WHERE repository_id=NEW.repository_id FOR UPDATE;
    IF quota IS NULL OR quota = 0 THEN RETURN NEW; END IF;
    SELECT COALESCE(SUM(size), 0) INTO used FROM native_cargo_publications
        WHERE repository_id=NEW.repository_id;
    IF used > quota THEN
        RAISE EXCEPTION 'repository capacity quota exceeded' USING ERRCODE='P0001';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER cargo_publication_capacity_check
    AFTER INSERT ON native_cargo_publications
    FOR EACH ROW EXECUTE FUNCTION assert_cargo_repository_capacity();

-- +goose Down
-- Cargo Hosted publication is forward-only; compensate with a later migration.
