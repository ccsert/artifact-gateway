-- +goose Up
ALTER TABLE native_go_assets
    ADD COLUMN IF NOT EXISTS collecting_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS collected_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS native_go_assets_reclaim_idx
    ON native_go_assets (repository_id, collected_at, module_path, version);

CREATE INDEX IF NOT EXISTS native_go_assets_object_reclaim_idx
    ON native_go_assets (object_key, module_path, version)
    WHERE collected_at IS NULL;

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
        SELECT COALESCE(SUM(size),0) INTO used FROM native_go_assets WHERE repository_id=p_repository_id AND collected_at IS NULL;
    ELSE RETURN;
    END CASE;
    IF used > quota THEN
        RAISE EXCEPTION 'repository capacity quota exceeded' USING ERRCODE='P0001';
    END IF;
END;
$$ LANGUAGE plpgsql;

-- +goose Down
-- Native Go retention is forward-only; compensate with a later migration.
