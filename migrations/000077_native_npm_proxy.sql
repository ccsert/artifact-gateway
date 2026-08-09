-- +goose Up
ALTER TABLE native_npm_packages
    ADD COLUMN IF NOT EXISTS source_endpoint TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS upstream_etag TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS upstream_modified TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS metadata_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS negative_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS negative BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE native_npm_versions
    ADD COLUMN IF NOT EXISTS upstream_tarball TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS cached_at TIMESTAMPTZ;

ALTER TABLE native_npm_versions DROP CONSTRAINT IF EXISTS native_npm_versions_digest_check;
ALTER TABLE native_npm_versions DROP CONSTRAINT IF EXISTS native_npm_versions_integrity_check;
ALTER TABLE native_npm_versions DROP CONSTRAINT IF EXISTS native_npm_versions_shasum_check;
ALTER TABLE native_npm_versions DROP CONSTRAINT IF EXISTS native_npm_versions_size_check;

ALTER TABLE native_npm_versions
    ALTER COLUMN digest DROP NOT NULL,
    ALTER COLUMN digest SET DEFAULT '',
    ALTER COLUMN integrity DROP NOT NULL,
    ALTER COLUMN integrity SET DEFAULT '',
    ALTER COLUMN shasum DROP NOT NULL,
    ALTER COLUMN shasum SET DEFAULT '',
    ALTER COLUMN object_key SET DEFAULT '',
    ALTER COLUMN size SET DEFAULT 0;

UPDATE native_npm_versions SET digest='' WHERE digest IS NULL;
UPDATE native_npm_versions SET cached_at=created_at WHERE object_key<>'' AND cached_at IS NULL;
ALTER TABLE native_npm_versions ALTER COLUMN digest SET NOT NULL;

ALTER TABLE native_npm_versions
    ADD CONSTRAINT native_npm_versions_digest_check
        CHECK (digest = '' OR digest ~ '^sha256:[0-9a-f]{64}$'),
    ADD CONSTRAINT native_npm_versions_integrity_check
        CHECK (integrity = '' OR integrity ~ '^sha512-[A-Za-z0-9+/]+={0,2}$'),
    ADD CONSTRAINT native_npm_versions_shasum_check
        CHECK (shasum = '' OR shasum ~ '^[0-9a-f]{40}$'),
    ADD CONSTRAINT native_npm_versions_size_check CHECK (size >= 0),
    ADD CONSTRAINT native_npm_versions_cached_object_check
        CHECK ((object_key = '' AND digest = '' AND size = 0 AND cached_at IS NULL)
            OR (object_key <> '' AND digest <> '' AND size > 0 AND cached_at IS NOT NULL));

DROP INDEX IF EXISTS native_npm_versions_digest_idx;
CREATE INDEX native_npm_versions_digest_idx
    ON native_npm_versions (repository_id, digest, package_name, version)
    WHERE digest <> '';
CREATE INDEX native_npm_packages_proxy_expiry_idx
    ON native_npm_packages (repository_id, metadata_expires_at, negative_expires_at)
    WHERE source_endpoint <> '';

CREATE OR REPLACE VIEW artifact_search_projection AS
SELECT a.repository_id,
       'maven'::text AS format,
       a.coordinate,
       a.digest,
       a.created_at,
       NULL::bigint AS size,
       COALESCE(p.publisher, '') AS publisher,
       a.build_number,
       NULL::text AS content_type,
       split_part(a.coordinate, ':', 3) AS version
FROM native_maven_artifacts a
LEFT JOIN LATERAL (
    SELECT s.publisher
    FROM native_maven_publish_sessions s
    WHERE s.repository_id = a.repository_id
      AND s.coordinate = a.coordinate
      AND s.state = 'committed'
    ORDER BY s.expires_at DESC
    LIMIT 1
) p ON true
WHERE a.state = 'visible'
UNION ALL
SELECT m.repository_id,
       'oci'::text AS format,
       m.name AS coordinate,
       m.digest,
       m.created_at,
       m.size,
       ''::text AS publisher,
       0 AS build_number,
       m.media_type AS content_type,
       NULL::text AS version
FROM native_oci_manifests m
UNION ALL
SELECT r.repository_id,
       'conan'::text AS format,
       r.reference AS coordinate,
       r.digest,
       r.created_at,
       NULL::bigint AS size,
       COALESCE(p.publisher, '') AS publisher,
       0 AS build_number,
       NULL::text AS content_type,
       split_part(r.reference, '/', 2) AS version
FROM native_conan_recipe_revisions r
LEFT JOIN LATERAL (
    SELECT s.publisher
    FROM native_conan_publish_sessions s
    WHERE s.repository_id = r.repository_id
      AND s.reference = r.reference
      AND s.state = 'committed'
    ORDER BY s.expires_at DESC
    LIMIT 1
) p ON true
WHERE r.state = 'visible'
UNION ALL
SELECT a.repository_id,
       'raw'::text AS format,
       a.path AS coordinate,
       a.digest,
       a.updated_at AS created_at,
       o.size,
       ''::text AS publisher,
       0 AS build_number,
       a.content_type,
       NULL::text AS version
FROM native_raw_assets a
JOIN native_raw_objects o ON o.digest = a.digest
UNION ALL
SELECT v.repository_id,
       'npm'::text AS format,
       v.package_name AS coordinate,
       v.digest,
       v.created_at,
       v.size,
       v.publisher,
       0 AS build_number,
       'application/octet-stream'::text AS content_type,
       v.version
FROM native_npm_versions v
JOIN native_npm_packages p
  ON p.repository_id=v.repository_id AND p.name=v.package_name
WHERE NOT p.negative;

COMMENT ON COLUMN native_npm_packages.source_endpoint IS
    'Configured Proxy Repository endpoint; empty for Hosted publications';
COMMENT ON COLUMN native_npm_versions.upstream_tarball IS
    'Validated upstream tarball URL retained until its verified local object is cached';

-- +goose Down
-- npm Proxy metadata is forward-only; compensate with a later migration.
