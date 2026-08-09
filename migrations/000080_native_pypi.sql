-- +goose Up
ALTER TABLE hosted_repositories DROP CONSTRAINT IF EXISTS hosted_repositories_format_check;
ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi'));

ALTER TABLE hosted_groups DROP CONSTRAINT IF EXISTS hosted_groups_format_check;
ALTER TABLE hosted_groups ADD CONSTRAINT hosted_groups_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi'));

ALTER TABLE artifact_tombstones DROP CONSTRAINT IF EXISTS artifact_tombstones_format_check;
ALTER TABLE artifact_tombstones ADD CONSTRAINT artifact_tombstones_format_check
    CHECK (format IN ('raw', 'oci', 'maven', 'conan', 'npm', 'pypi'));

CREATE TABLE IF NOT EXISTS native_pypi_files (
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
    project TEXT NOT NULL CHECK (project ~ '^[a-z0-9]([a-z0-9-]{0,253}[a-z0-9])?$'),
    version TEXT NOT NULL,
    filename TEXT NOT NULL,
    file_type TEXT NOT NULL DEFAULT '',
    python_version TEXT NOT NULL DEFAULT '',
    requires_python TEXT NOT NULL DEFAULT '',
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL DEFAULT '',
    size BIGINT NOT NULL DEFAULT 0 CHECK (size >= 0),
    publisher TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'visible' CHECK (state IN ('visible', 'deleted')),
    cached_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    collected_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (repository_id, filename),
    CHECK ((object_key='' AND size=0 AND cached_at IS NULL) OR
           (object_key<>'' AND size>0 AND cached_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS native_pypi_files_project_idx
    ON native_pypi_files (repository_id, project text_pattern_ops, created_at DESC)
    WHERE state='visible';
CREATE INDEX IF NOT EXISTS native_pypi_files_digest_idx
    ON native_pypi_files (repository_id, digest, project, version)
    WHERE state='visible';
CREATE INDEX IF NOT EXISTS native_pypi_files_reclaim_idx
    ON native_pypi_files (deleted_at, object_key)
    WHERE state='deleted' AND collected_at IS NULL AND object_key<>'';

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
    ELSE RETURN;
    END CASE;
    IF used > quota THEN
        RAISE EXCEPTION 'repository capacity quota exceeded' USING ERRCODE='P0001';
    END IF;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE TRIGGER pypi_file_capacity_check
    AFTER INSERT OR UPDATE ON native_pypi_files
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
) projection;

COMMENT ON VIEW artifact_search_projection IS
    'Format-neutral versioned metadata projection for authorized coordinate and digest search';

-- +goose Down
-- PyPI support is forward-only; compensate with a later migration.
