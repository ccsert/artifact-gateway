-- +goose Up
-- RubyGems remains opt-in and outside the admitted management Format enum.
ALTER TABLE hosted_repositories DROP CONSTRAINT IF EXISTS hosted_repositories_format_check;
ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_format_check
 CHECK(format IN ('raw','oci','maven','conan','npm','pypi','go','apt','cargo','nuget','helm','rubygems'));

CREATE TABLE native_rubygems_sessions (
 id UUID PRIMARY KEY,
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 128),
 version TEXT NOT NULL CHECK(length(version) BETWEEN 1 AND 128),
 canonical_version TEXT NOT NULL,
 platform TEXT NOT NULL CHECK(length(platform) BETWEEN 1 AND 128),
 full_name TEXT NOT NULL,
 digest TEXT NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 size BIGINT NOT NULL CHECK(size BETWEEN 1 AND 67108864),
 actor TEXT NOT NULL CHECK(actor<>''),
 state TEXT NOT NULL CHECK(state IN ('pending','committed','expired')),
 object_key TEXT NOT NULL UNIQUE,
 metadata_json TEXT NOT NULL CHECK(jsonb_typeof(metadata_json::jsonb)='object'),
 metadata_yaml BYTEA NOT NULL CHECK(octet_length(metadata_yaml) BETWEEN 1 AND 1048576),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL,
 committed_at TIMESTAMPTZ,
 collected_at TIMESTAMPTZ,
 CHECK((state='committed')=(committed_at IS NOT NULL)),
 CHECK(collected_at IS NULL OR state='expired')
);
CREATE UNIQUE INDEX native_rubygems_active_identity
 ON native_rubygems_sessions(repository_id,name,canonical_version,platform)
 WHERE state IN ('pending','committed');
CREATE UNIQUE INDEX native_rubygems_visible_filename
 ON native_rubygems_sessions(repository_id,full_name) WHERE state='committed';
CREATE INDEX native_rubygems_expiry ON native_rubygems_sessions(expires_at,id)
 WHERE collected_at IS NULL AND state<>'committed';
CREATE TABLE native_rubygems_quotas (
 repository_id UUID PRIMARY KEY REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 quota_bytes BIGINT NOT NULL CHECK(quota_bytes>=0)
);
-- +goose Down
-- Forward-only: immutable publication identities and their bytes are retained.
