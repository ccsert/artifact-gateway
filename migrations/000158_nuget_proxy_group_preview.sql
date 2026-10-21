-- +goose Up
-- Source configuration versions are separate immutable cache namespaces. This
-- remains opt-in preview storage, not public format admission.
ALTER TABLE native_nuget_publication_sessions
 ADD COLUMN source_version TEXT NOT NULL DEFAULT '',
 ADD COLUMN source_url TEXT NOT NULL DEFAULT '',
 ADD COLUMN registration_url TEXT NOT NULL DEFAULT '';
ALTER TABLE native_nuget_publications
 ADD COLUMN source_version TEXT NOT NULL DEFAULT '',
 ADD COLUMN source_url TEXT NOT NULL DEFAULT '',
 ADD COLUMN registration_url TEXT NOT NULL DEFAULT '';
ALTER TABLE native_nuget_publications DROP CONSTRAINT native_nuget_publications_pkey;
ALTER TABLE native_nuget_publications ADD PRIMARY KEY(repository_id,package_id,version,source_version);
DROP INDEX native_nuget_active_identity;
CREATE UNIQUE INDEX native_nuget_active_identity ON native_nuget_publication_sessions(repository_id,package_id,version,source_version)
 WHERE state IN ('reserved','staged','committed');

ALTER TABLE hosted_groups DROP CONSTRAINT IF EXISTS hosted_groups_format_check;
ALTER TABLE hosted_groups ADD CONSTRAINT hosted_groups_format_check
 CHECK(format IN ('raw','oci','maven','conan','npm','pypi','go','apt','cargo','nuget'));

CREATE TABLE native_nuget_source_documents (
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 source_version TEXT NOT NULL CHECK(source_version ~ '^[1-9][0-9]*$'),
 url TEXT NOT NULL CHECK(length(url) BETWEEN 1 AND 4096),
 status INTEGER NOT NULL CHECK(status IN (200,404)),
 body BYTEA NOT NULL CHECK(octet_length(body)<=8388608),
 digest TEXT NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 etag TEXT NOT NULL,
 last_modified TEXT NOT NULL,
 fetched_at TIMESTAMPTZ NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(repository_id,source_version,url),
 CHECK(expires_at>fetched_at),
 CHECK(status=200 OR octet_length(body)=0)
);
CREATE TABLE native_nuget_group_authorities (
 group_id UUID NOT NULL REFERENCES hosted_groups(id) ON DELETE CASCADE,
 package_id TEXT NOT NULL CHECK(length(package_id) BETWEEN 1 AND 100 AND package_id ~ '^[a-z0-9_]+([.-][a-z0-9_]+)*$'),
 version TEXT NOT NULL CHECK(length(version) BETWEEN 1 AND 256 AND version=lower(version)),
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id),
 repository_version TEXT NOT NULL CHECK(repository_version ~ '^[1-9][0-9]*$'),
 digest TEXT NOT NULL CHECK(digest='' OR digest ~ '^sha256:[0-9a-f]{64}$'),
 size BIGINT NOT NULL CHECK(size>=0),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(group_id,package_id,version),
 CHECK((digest='')=(size=0))
);

-- +goose Down
-- Forward-only: durable authorities and immutable cache references are retained.
