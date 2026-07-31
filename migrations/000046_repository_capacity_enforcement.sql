-- +goose Up
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
    ELSE
        RETURN;
    END CASE;
    IF used > quota THEN
        RAISE EXCEPTION 'repository capacity quota exceeded' USING ERRCODE='P0001';
    END IF;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION assert_hosted_repository_capacity_trigger() RETURNS TRIGGER AS $$
BEGIN
    PERFORM assert_hosted_repository_capacity(NEW.repository_id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE TRIGGER repository_capacity_quota_check AFTER INSERT OR UPDATE OF quota_bytes ON repository_capacity_quotas FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();
CREATE OR REPLACE TRIGGER raw_asset_capacity_check AFTER INSERT OR UPDATE ON native_raw_assets FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();
CREATE OR REPLACE TRIGGER maven_asset_capacity_check AFTER INSERT OR UPDATE ON native_maven_assets FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();
CREATE OR REPLACE TRIGGER maven_artifact_capacity_check AFTER INSERT OR UPDATE OF state ON native_maven_artifacts FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();
CREATE OR REPLACE TRIGGER oci_blob_capacity_check AFTER INSERT OR UPDATE ON native_oci_repository_blobs FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();
CREATE OR REPLACE TRIGGER oci_manifest_capacity_check AFTER INSERT OR UPDATE ON native_oci_manifests FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();
CREATE OR REPLACE TRIGGER conan_asset_capacity_check AFTER INSERT OR UPDATE ON native_conan_assets FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();
CREATE OR REPLACE TRIGGER conan_recipe_capacity_check AFTER INSERT OR UPDATE OF state ON native_conan_recipe_revisions FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();
CREATE OR REPLACE TRIGGER conan_package_capacity_check AFTER INSERT OR UPDATE OF state ON native_conan_package_revisions FOR EACH ROW EXECUTE FUNCTION assert_hosted_repository_capacity_trigger();

-- +goose Down
DROP TRIGGER conan_package_capacity_check ON native_conan_package_revisions;
DROP TRIGGER conan_recipe_capacity_check ON native_conan_recipe_revisions;
DROP TRIGGER conan_asset_capacity_check ON native_conan_assets;
DROP TRIGGER oci_manifest_capacity_check ON native_oci_manifests;
DROP TRIGGER oci_blob_capacity_check ON native_oci_repository_blobs;
DROP TRIGGER maven_artifact_capacity_check ON native_maven_artifacts;
DROP TRIGGER maven_asset_capacity_check ON native_maven_assets;
DROP TRIGGER raw_asset_capacity_check ON native_raw_assets;
DROP TRIGGER repository_capacity_quota_check ON repository_capacity_quotas;
DROP FUNCTION assert_hosted_repository_capacity_trigger();
DROP FUNCTION assert_hosted_repository_capacity(UUID);
