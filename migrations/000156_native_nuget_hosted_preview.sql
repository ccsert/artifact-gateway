-- +goose Up
-- NuGet remains an explicit Hosted preview, absent from public format profiles.
ALTER TABLE hosted_repositories DROP CONSTRAINT IF EXISTS hosted_repositories_format_check;
ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_format_check
 CHECK (format IN ('raw','oci','maven','conan','npm','pypi','go','apt','cargo','nuget'));

CREATE TABLE native_nuget_publication_sessions (
 id UUID PRIMARY KEY,
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 package_id TEXT NOT NULL CHECK (length(package_id) BETWEEN 1 AND 100 AND package_id ~ '^[a-z0-9_]+([.-][a-z0-9_]+)*$'),
 version TEXT NOT NULL CHECK (length(version) BETWEEN 1 AND 256 AND version=lower(version)),
 digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
 size BIGINT NOT NULL CHECK (size > 0 AND size <= 268435456),
 actor TEXT NOT NULL CHECK (actor<>''),
 state TEXT NOT NULL CHECK (state IN ('reserved','staged','committed','expired')),
 object_key TEXT NOT NULL UNIQUE,
 metadata_json TEXT NOT NULL DEFAULT '',
 nuspec BYTEA NOT NULL DEFAULT ''::bytea,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL,
 committed_at TIMESTAMPTZ,
 collected_at TIMESTAMPTZ,
 CHECK (collected_at IS NULL OR state='expired'),
 CHECK (state NOT IN ('staged','committed') OR (length(metadata_json)>0 AND jsonb_typeof(metadata_json::jsonb)='object' AND octet_length(nuspec)>0)),
 CHECK ((state='committed')=(committed_at IS NOT NULL))
);
CREATE UNIQUE INDEX native_nuget_active_identity ON native_nuget_publication_sessions(repository_id,package_id,version)
 WHERE state IN ('reserved','staged','committed');
CREATE INDEX native_nuget_expiry ON native_nuget_publication_sessions(expires_at,id) WHERE state IN ('reserved','staged');

CREATE TABLE native_nuget_publications (
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 package_id TEXT NOT NULL,
 version TEXT NOT NULL,
 session_id UUID NOT NULL UNIQUE REFERENCES native_nuget_publication_sessions(id),
 digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
 object_key TEXT NOT NULL,
 size BIGINT NOT NULL CHECK(size>0),
 metadata_json TEXT NOT NULL CHECK(jsonb_typeof(metadata_json::jsonb)='object'),
 nuspec BYTEA NOT NULL CHECK(octet_length(nuspec)>0),
 publisher TEXT NOT NULL,
 published_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(repository_id,package_id,version)
);

-- +goose Down
-- Forward-only. Publication identities and verified bytes are not downgraded.
