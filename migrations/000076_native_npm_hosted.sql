-- +goose Up
ALTER TABLE hosted_repositories DROP CONSTRAINT IF EXISTS hosted_repositories_format_check;
ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm'));

CREATE TABLE IF NOT EXISTS native_npm_packages (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    name TEXT NOT NULL,
    dist_tags JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(dist_tags) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, name)
);

CREATE TABLE IF NOT EXISTS native_npm_versions (
    repository_id UUID NOT NULL,
    package_name TEXT NOT NULL,
    version TEXT NOT NULL,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    integrity TEXT NOT NULL CHECK (integrity ~ '^sha512-[A-Za-z0-9+/]+={0,2}$'),
    shasum TEXT NOT NULL CHECK (shasum ~ '^[0-9a-f]{40}$'),
    tarball_name TEXT NOT NULL,
    object_key TEXT NOT NULL,
    size BIGINT NOT NULL CHECK (size > 0),
    manifest JSONB NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
    publisher TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, package_name, version),
    UNIQUE (repository_id, package_name, tarball_name),
    FOREIGN KEY (repository_id, package_name)
        REFERENCES native_npm_packages(repository_id, name)
        ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS native_npm_versions_digest_idx
    ON native_npm_versions (repository_id, digest, package_name, version);
CREATE INDEX IF NOT EXISTS native_npm_packages_name_idx
    ON native_npm_packages (repository_id, name text_pattern_ops);

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
FROM native_npm_versions v;

CREATE OR REPLACE FUNCTION assert_hosted_repository_capacity(p_repository_id UUID) RETURNS VOID AS $$
DECLARE
    quota BIGINT;
    repository_format TEXT;
    used BIGINT;
BEGIN
    SELECT quota_bytes INTO quota FROM repository_capacity_quotas WHERE repository_id=p_repository_id FOR UPDATE;
    IF quota IS NULL OR quota = 0 THEN
        RETURN;
    END IF;
    SELECT format INTO repository_format FROM hosted_repositories WHERE id=p_repository_id;
    CASE repository_format
    WHEN 'raw' THEN
        SELECT COALESCE(SUM(o.size), 0) INTO used FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest WHERE a.repository_id=p_repository_id;
    WHEN 'maven' THEN
        SELECT COALESCE(SUM(a.size), 0) INTO used FROM native_maven_assets a WHERE a.repository_id=p_repository_id AND EXISTS (SELECT 1 FROM native_maven_artifacts m WHERE m.repository_id=a.repository_id AND m.state='visible' AND left(a.path, length(replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/')) = replace(split_part(m.coordinate, ':', 1), '.', '/') || '/' || split_part(m.coordinate, ':', 2) || '/' || split_part(m.coordinate, ':', 3) || '/');
    WHEN 'oci' THEN
        SELECT COALESCE((SELECT SUM(size) FROM native_oci_manifests WHERE repository_id=p_repository_id), 0) + COALESCE((SELECT SUM(b.size) FROM native_oci_repository_blobs rb JOIN native_oci_blobs b ON b.digest=rb.digest WHERE rb.repository_id=p_repository_id), 0) INTO used;
    WHEN 'conan' THEN
        SELECT COALESCE(SUM(a.size), 0) INTO used FROM native_conan_assets a JOIN native_conan_recipe_revisions r ON r.repository_id=a.repository_id AND r.reference=a.reference AND r.revision=a.recipe_revision LEFT JOIN native_conan_package_revisions p ON p.repository_id=a.repository_id AND p.reference=a.reference AND p.recipe_revision=a.recipe_revision AND p.package_id=a.package_id AND p.revision=a.package_revision WHERE a.repository_id=p_repository_id AND r.state='visible' AND (a.package_id='' OR p.state='visible');
    WHEN 'npm' THEN
        SELECT COALESCE(SUM(size), 0) INTO used FROM native_npm_versions WHERE repository_id=p_repository_id;
    ELSE
        RETURN;
    END CASE;
    IF used > quota THEN
        RAISE EXCEPTION 'repository capacity quota exceeded' USING ERRCODE='P0001';
    END IF;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE TRIGGER npm_version_capacity_check
    AFTER INSERT OR UPDATE ON native_npm_versions
    FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();

COMMENT ON VIEW artifact_search_projection IS
    'Format-neutral versioned metadata projection for authorized coordinate and digest search';

-- +goose Down
-- npm support is forward-only; compensate with a later migration.
