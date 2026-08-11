-- +goose Up
ALTER TABLE hosted_repositories DROP CONSTRAINT IF EXISTS hosted_repositories_format_check;
ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go', 'apt'));

ALTER TABLE hosted_groups DROP CONSTRAINT IF EXISTS hosted_groups_format_check;
ALTER TABLE hosted_groups ADD CONSTRAINT hosted_groups_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go', 'apt'));

ALTER TABLE artifact_tombstones DROP CONSTRAINT IF EXISTS artifact_tombstones_format_check;
ALTER TABLE artifact_tombstones ADD CONSTRAINT artifact_tombstones_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi', 'go', 'apt'));

CREATE TABLE IF NOT EXISTS native_apt_assets (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    path TEXT NOT NULL CHECK (path <> '' AND length(path) <= 2048 AND path !~ '(^|/)[.][.]?(/|$)'),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL CHECK (object_key <> ''),
    size BIGINT NOT NULL CHECK (size >= 0),
    content_type TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL,
    upstream_etag TEXT NOT NULL DEFAULT '',
    upstream_modified TEXT NOT NULL DEFAULT '',
    cached_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, path)
);
CREATE INDEX IF NOT EXISTS native_apt_assets_path_idx
    ON native_apt_assets (repository_id, path text_pattern_ops);
CREATE INDEX IF NOT EXISTS native_apt_assets_digest_idx
    ON native_apt_assets (repository_id, digest, path);

CREATE OR REPLACE FUNCTION assert_apt_repository_capacity_trigger() RETURNS trigger AS $$
DECLARE
    quota BIGINT;
    used BIGINT;
BEGIN
    SELECT quota_bytes INTO quota FROM repository_capacity_quotas WHERE repository_id=NEW.repository_id FOR UPDATE;
    IF quota IS NULL OR quota = 0 THEN RETURN NEW; END IF;
    SELECT COALESCE(SUM(size),0) INTO used FROM native_apt_assets WHERE repository_id=NEW.repository_id;
    IF used > quota THEN
        RAISE EXCEPTION 'repository capacity quota exceeded' USING ERRCODE='P0001';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS apt_asset_capacity_check ON native_apt_assets;
CREATE TRIGGER apt_asset_capacity_check
    AFTER INSERT OR UPDATE ON native_apt_assets
    FOR EACH ROW EXECUTE FUNCTION assert_apt_repository_capacity_trigger();

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
    UNION ALL SELECT a.repository_id,'apt',a.path,a.digest,a.created_at,a.size,''::text,0,a.content_type,NULL
    FROM native_apt_assets a
) projection;

COMMENT ON VIEW artifact_search_projection IS
    'Format-neutral versioned metadata projection for authorized coordinate and digest search';

-- +goose Down
-- APT Proxy support is forward-only; compensate with a later migration.
