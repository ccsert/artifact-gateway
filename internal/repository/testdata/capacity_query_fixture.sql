-- Session-local tables mirror only columns used by the capacity query.
-- Every test uses a separate connection; no persistent schema or rows are changed.
CREATE TEMP TABLE hosted_repositories (id uuid PRIMARY KEY,name text,format text,repo_type text,endpoint text,state text);
CREATE TEMP TABLE repository_capacity_quotas(repository_id uuid PRIMARY KEY,quota_bytes bigint);
CREATE TEMP TABLE native_raw_assets(repository_id uuid,digest text); CREATE TEMP TABLE native_raw_objects(digest text PRIMARY KEY,size bigint);
CREATE TEMP TABLE native_maven_assets(repository_id uuid,path text,size bigint,PRIMARY KEY(repository_id,path));
CREATE TEMP TABLE native_maven_artifacts(repository_id uuid,coordinate text,state text,build_number int);
CREATE TEMP TABLE native_oci_manifests(repository_id uuid,size bigint);
CREATE TEMP TABLE native_oci_repository_blobs(repository_id uuid,digest text); CREATE TEMP TABLE native_oci_blobs(digest text PRIMARY KEY,size bigint);
CREATE TEMP TABLE native_conan_assets(repository_id uuid,reference text,recipe_revision text,package_id text,package_revision text,size bigint);
CREATE TEMP TABLE native_conan_recipe_revisions(repository_id uuid,reference text,revision text,state text);
CREATE TEMP TABLE native_conan_package_revisions(repository_id uuid,reference text,recipe_revision text,package_id text,revision text,state text);
CREATE TEMP TABLE native_npm_versions(repository_id uuid,size bigint,object_key text);
CREATE TEMP TABLE native_pypi_files(repository_id uuid,size bigint,object_key text,state text);
CREATE TEMP TABLE native_go_assets(repository_id uuid,size bigint,collected_at timestamptz);
CREATE TEMP TABLE native_cargo_publications(repository_id uuid,size bigint,collected_at timestamptz);
CREATE TEMP TABLE native_apt_assets(repository_id uuid,size bigint); CREATE TEMP TABLE native_apt_package_revisions(repository_id uuid,size bigint);
CREATE TEMP TABLE native_apt_repository_snapshots(id uuid,repository_id uuid,state text);
CREATE TEMP TABLE native_apt_snapshot_assets(snapshot_id uuid,repository_id uuid,object_key text,size bigint,path text);
CREATE TEMP TABLE resolver_audit_log(repository text,format text,outcome text,occurred_at timestamptz);
