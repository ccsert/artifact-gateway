-- +goose Up
ALTER TABLE hosted_groups DROP CONSTRAINT IF EXISTS hosted_groups_format_check;
ALTER TABLE hosted_groups ADD CONSTRAINT hosted_groups_format_check
 CHECK(format IN ('raw','oci','maven','conan','npm','pypi','go','apt','cargo','nuget','helm'));
-- Default-off Helm preview: bounded source metadata and immutable source views.
ALTER TABLE native_helm_publication_sessions ADD COLUMN source_version text NOT NULL DEFAULT '';
CREATE TABLE native_helm_source_documents (
 repository_id uuid PRIMARY KEY REFERENCES hosted_repositories(id),
 repository_version text NOT NULL,
 body bytea NOT NULL CHECK(octet_length(body) BETWEEN 1 AND 1048576),
 digest text NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 etag text NOT NULL DEFAULT '', last_modified text NOT NULL DEFAULT '',
 fetched_at timestamptz NOT NULL, expires_at timestamptz NOT NULL
);
CREATE TABLE native_helm_source_snapshots (
 id uuid PRIMARY KEY, view_id uuid NOT NULL, view_version text NOT NULL,
 digest text NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 entries_json jsonb NOT NULL CHECK(octet_length(entries_json::text)<=8388608),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX native_helm_source_snapshots_view ON native_helm_source_snapshots(view_id);
CREATE TABLE native_helm_group_authorities (
 group_id uuid NOT NULL REFERENCES hosted_groups(id), name text NOT NULL, version text NOT NULL,
 entry_json jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(group_id,name,version)
);
