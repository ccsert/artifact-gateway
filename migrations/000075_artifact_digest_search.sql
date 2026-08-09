-- +goose Up
-- Preserve every visible version in the shared projection. Coordinate-prefix
-- searches collapse versioned formats in the query, while digest searches can
-- locate an older OCI manifest or Conan recipe revision exactly.
CREATE OR REPLACE VIEW artifact_search_projection AS
SELECT a.repository_id,
       'maven'::text AS format,
       a.coordinate,
       a.digest,
       a.created_at,
       NULL::bigint AS size,
       COALESCE(p.publisher, '') AS publisher,
       a.build_number,
       NULL::text AS content_type
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
       m.media_type AS content_type
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
       NULL::text AS content_type
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
       a.content_type
FROM native_raw_assets a
JOIN native_raw_objects o ON o.digest = a.digest;

CREATE INDEX IF NOT EXISTS native_maven_artifacts_digest_search_idx
    ON native_maven_artifacts (repository_id, digest, coordinate, build_number)
    WHERE state = 'visible';
CREATE INDEX IF NOT EXISTS native_oci_manifests_digest_search_idx
    ON native_oci_manifests (repository_id, digest, name, created_at DESC);
CREATE INDEX IF NOT EXISTS native_conan_recipe_revisions_digest_search_idx
    ON native_conan_recipe_revisions (repository_id, digest, reference, created_at DESC)
    WHERE state = 'visible';
CREATE INDEX IF NOT EXISTS native_raw_assets_repository_digest_search_idx
    ON native_raw_assets (repository_id, digest, path);

COMMENT ON VIEW artifact_search_projection IS
    'Format-neutral versioned metadata projection for authorized coordinate and digest search';

-- +goose Down
DROP INDEX IF EXISTS native_raw_assets_repository_digest_search_idx;
DROP INDEX IF EXISTS native_conan_recipe_revisions_digest_search_idx;
DROP INDEX IF EXISTS native_oci_manifests_digest_search_idx;
DROP INDEX IF EXISTS native_maven_artifacts_digest_search_idx;

CREATE OR REPLACE VIEW artifact_search_projection AS
SELECT a.repository_id,
       'maven'::text AS format,
       a.coordinate,
       a.digest,
       a.created_at,
       NULL::bigint AS size,
       COALESCE(p.publisher, '') AS publisher,
       a.build_number,
       NULL::text AS content_type
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
       m.media_type AS content_type
FROM (
    SELECT DISTINCT ON (repository_id, name)
           repository_id, name, digest, created_at, size, media_type
    FROM native_oci_manifests
    ORDER BY repository_id, name, created_at DESC, digest DESC
) m
UNION ALL
SELECT r.repository_id,
       'conan'::text AS format,
       r.reference AS coordinate,
       r.digest,
       r.created_at,
       NULL::bigint AS size,
       COALESCE(p.publisher, '') AS publisher,
       0 AS build_number,
       NULL::text AS content_type
FROM (
    SELECT DISTINCT ON (repository_id, reference)
           repository_id, reference, revision, digest, created_at
    FROM native_conan_recipe_revisions
    WHERE state = 'visible'
    ORDER BY repository_id, reference, created_at DESC, revision DESC
) r
LEFT JOIN LATERAL (
    SELECT s.publisher
    FROM native_conan_publish_sessions s
    WHERE s.repository_id = r.repository_id
      AND s.reference = r.reference
      AND s.state = 'committed'
    ORDER BY s.expires_at DESC
    LIMIT 1
) p ON true
UNION ALL
SELECT a.repository_id,
       'raw'::text AS format,
       a.path AS coordinate,
       a.digest,
       a.updated_at AS created_at,
       o.size,
       ''::text AS publisher,
       0 AS build_number,
       a.content_type
FROM native_raw_assets a
JOIN native_raw_objects o ON o.digest = a.digest;

COMMENT ON VIEW artifact_search_projection IS
    'Format-neutral metadata projection for authorized management search';
