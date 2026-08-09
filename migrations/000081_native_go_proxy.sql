-- +goose Up
ALTER TABLE hosted_repositories DROP CONSTRAINT IF EXISTS hosted_repositories_format_check;
ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go'));

ALTER TABLE hosted_groups DROP CONSTRAINT IF EXISTS hosted_groups_format_check;
ALTER TABLE hosted_groups ADD CONSTRAINT hosted_groups_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go'));

ALTER TABLE artifact_tombstones DROP CONSTRAINT IF EXISTS artifact_tombstones_format_check;
ALTER TABLE artifact_tombstones ADD CONSTRAINT artifact_tombstones_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go'));

CREATE TABLE IF NOT EXISTS native_go_versions (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    module_path TEXT NOT NULL CHECK (module_path <> '' AND length(module_path) <= 1024),
    version TEXT NOT NULL CHECK (version <> '' AND length(version) <= 512),
    published_at TIMESTAMPTZ,
    publisher TEXT NOT NULL DEFAULT '',
    cached_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, module_path, version)
);
CREATE INDEX IF NOT EXISTS native_go_versions_module_idx
    ON native_go_versions (repository_id, module_path text_pattern_ops, version);

CREATE TABLE IF NOT EXISTS native_go_assets (
    repository_id UUID NOT NULL,
    module_path TEXT NOT NULL,
    version TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('info', 'mod', 'zip')),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL CHECK (object_key <> ''),
    size BIGINT NOT NULL CHECK (size > 0),
    source_url TEXT NOT NULL,
    cached_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, module_path, version, kind),
    FOREIGN KEY (repository_id, module_path, version)
        REFERENCES native_go_versions(repository_id, module_path, version) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS native_go_assets_digest_idx
    ON native_go_assets (repository_id, digest, module_path, version);

CREATE OR REPLACE FUNCTION assert_hosted_repository_capacity(p_repository_id UUID) RETURNS VOID AS $$
DECLARE
    quota BIGINT;
    repository_format TEXT;
    used BIGINT;
BEGIN
    SELECT quota_bytes INTO quota FROM repository_capacity_quotas WHERE repository_id=p_repository_id FOR UPDATE;
    IF quota IS NULL OR quota = 0 THEN RETURN; END IF;
    SELECT format INTO repository_format FROM hosted_repositories WHERE id=p_repository_id;
    CASE repository_format
    WHEN 'raw' THEN
        SELECT COALESCE(SUM(o.size),0) INTO used FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest WHERE a.repository_id=p_repository_id;
    WHEN 'maven' THEN
        SELECT COALESCE(SUM(a.size),0) INTO used FROM native_maven_assets a WHERE a.repository_id=p_repository_id AND EXISTS (SELECT 1 FROM native_maven_artifacts m WHERE m.repository_id=a.repository_id AND m.state='visible' AND left(a.path,length(replace(split_part(m.coordinate,':',1),'.','/') || '/' || split_part(m.coordinate,':',2) || '/' || split_part(m.coordinate,':',3) || '/'))=replace(split_part(m.coordinate,':',1),'.','/') || '/' || split_part(m.coordinate,':',2) || '/' || split_part(m.coordinate,':',3) || '/');
    WHEN 'oci' THEN
        SELECT COALESCE((SELECT SUM(size) FROM native_oci_manifests WHERE repository_id=p_repository_id),0)+COALESCE((SELECT SUM(b.size) FROM native_oci_repository_blobs rb JOIN native_oci_blobs b ON b.digest=rb.digest WHERE rb.repository_id=p_repository_id),0) INTO used;
    WHEN 'conan' THEN
        SELECT COALESCE(SUM(a.size),0) INTO used FROM native_conan_assets a JOIN native_conan_recipe_revisions r ON r.repository_id=a.repository_id AND r.reference=a.reference AND r.revision=a.recipe_revision LEFT JOIN native_conan_package_revisions p ON p.repository_id=a.repository_id AND p.reference=a.reference AND p.recipe_revision=a.recipe_revision AND p.package_id=a.package_id AND p.revision=a.package_revision WHERE a.repository_id=p_repository_id AND r.state='visible' AND (a.package_id='' OR p.state='visible');
    WHEN 'npm' THEN
        SELECT COALESCE(SUM(size),0) INTO used FROM native_npm_versions WHERE repository_id=p_repository_id AND state='visible';
    WHEN 'pypi' THEN
        SELECT COALESCE(SUM(size),0) INTO used FROM native_pypi_files WHERE repository_id=p_repository_id AND state='visible';
    WHEN 'go' THEN
        SELECT COALESCE(SUM(size),0) INTO used FROM native_go_assets WHERE repository_id=p_repository_id;
    ELSE RETURN;
    END CASE;
    IF used > quota THEN
        RAISE EXCEPTION 'repository capacity quota exceeded' USING ERRCODE='P0001';
    END IF;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE TRIGGER go_asset_capacity_check
    AFTER INSERT OR UPDATE ON native_go_assets
    FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();

CREATE OR REPLACE VIEW artifact_search_projection AS
SELECT * FROM (
    SELECT a.repository_id,'maven'::text AS format,a.coordinate,a.digest,a.created_at,NULL::bigint AS size,
           COALESCE(p.publisher,'') AS publisher,a.build_number,NULL::text AS content_type,split_part(a.coordinate,':',3) AS version
    FROM native_maven_artifacts a
    LEFT JOIN LATERAL (SELECT s.publisher FROM native_maven_publish_sessions s WHERE s.repository_id=a.repository_id AND s.coordinate=a.coordinate AND s.state='committed' ORDER BY s.expires_at DESC LIMIT 1) p ON true
    WHERE a.state='visible'
    UNION ALL SELECT m.repository_id,'oci',m.name,m.digest,m.created_at,m.size,'',0,m.media_type,NULL FROM native_oci_manifests m
    UNION ALL SELECT r.repository_id,'conan',r.reference,r.digest,r.created_at,NULL::bigint,COALESCE(p.publisher,''),0,NULL::text,split_part(r.reference,'/',2)
    FROM native_conan_recipe_revisions r
    LEFT JOIN LATERAL (SELECT s.publisher FROM native_conan_publish_sessions s WHERE s.repository_id=r.repository_id AND s.reference=r.reference AND s.state='committed' ORDER BY s.expires_at DESC LIMIT 1) p ON true
    WHERE r.state='visible'
    UNION ALL SELECT a.repository_id,'raw',a.path,a.digest,a.updated_at,o.size,'',0,a.content_type,NULL FROM native_raw_assets a JOIN native_raw_objects o ON o.digest=a.digest
    UNION ALL SELECT v.repository_id,'npm',v.package_name,v.digest,v.created_at,v.size,v.publisher,0,'application/octet-stream',v.version
    FROM native_npm_versions v JOIN native_npm_packages p ON p.repository_id=v.repository_id AND p.name=v.package_name
    WHERE NOT p.negative AND v.state='visible'
    UNION ALL SELECT f.repository_id,'pypi',f.project,f.digest,f.created_at,f.size,f.publisher,0,'application/octet-stream',f.version
    FROM native_pypi_files f WHERE f.state='visible'
    UNION ALL SELECT v.repository_id,'go',v.module_path,COALESCE(a.digest,''),v.created_at,a.size,v.publisher,0,
           CASE WHEN a.kind='zip' THEN 'application/zip' ELSE 'text/plain' END,v.version
    FROM native_go_versions v
    LEFT JOIN LATERAL (
        SELECT digest,size,kind FROM native_go_assets a
        WHERE a.repository_id=v.repository_id AND a.module_path=v.module_path AND a.version=v.version
        ORDER BY CASE a.kind WHEN 'zip' THEN 0 WHEN 'mod' THEN 1 ELSE 2 END LIMIT 1
    ) a ON true
) projection;

COMMENT ON VIEW artifact_search_projection IS
    'Format-neutral versioned metadata projection for authorized coordinate and digest search';

-- +goose Down
-- Go Proxy support is forward-only; compensate with a later migration.
