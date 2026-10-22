-- +goose Up
-- Helm is an explicitly enabled Hosted preview, outside the admitted catalog.
ALTER TABLE hosted_repositories DROP CONSTRAINT IF EXISTS hosted_repositories_format_check;
ALTER TABLE hosted_repositories ADD CONSTRAINT hosted_repositories_format_check
 CHECK (format IN ('raw','oci','maven','conan','npm','pypi','go','apt','cargo','nuget','helm'));

CREATE TABLE native_helm_publication_sessions (
 id UUID PRIMARY KEY,
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 name TEXT NOT NULL CHECK(length(name) BETWEEN 1 AND 63 AND name ~ '^[a-z0-9][a-z0-9-]*$'),
 version TEXT NOT NULL CHECK(length(version) BETWEEN 1 AND 256),
 digest TEXT NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 size BIGINT NOT NULL CHECK(size>0 AND size<=67108864),
 provenance_digest TEXT NOT NULL DEFAULT '',
 provenance_size BIGINT NOT NULL DEFAULT 0 CHECK(provenance_size>=0 AND provenance_size<=1048576),
 actor TEXT NOT NULL CHECK(actor<>''),
 state TEXT NOT NULL CHECK(state IN ('pending','committed','expired')),
 object_key TEXT NOT NULL UNIQUE,
 provenance_key TEXT NOT NULL DEFAULT '',
 metadata_json TEXT NOT NULL CHECK(jsonb_typeof(metadata_json::jsonb)='object'),
 chart_yaml BYTEA NOT NULL CHECK(octet_length(chart_yaml) BETWEEN 1 AND 1048576),
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 expires_at TIMESTAMPTZ NOT NULL,
 committed_at TIMESTAMPTZ,
 collected_at TIMESTAMPTZ,
 CHECK((provenance_size=0 AND provenance_digest='' AND provenance_key='') OR
       (provenance_size>0 AND provenance_digest ~ '^sha256:[0-9a-f]{64}$' AND provenance_key<>'')),
 CHECK(collected_at IS NULL OR state='expired'),
 CHECK((state='committed')=(committed_at IS NOT NULL))
);
CREATE UNIQUE INDEX native_helm_active_identity ON native_helm_publication_sessions(repository_id,name,version)
 WHERE state IN ('pending','committed');
CREATE INDEX native_helm_expiry ON native_helm_publication_sessions(expires_at,id) WHERE state='pending';
CREATE UNIQUE INDEX native_helm_provenance_key ON native_helm_publication_sessions(provenance_key) WHERE provenance_key<>'';

CREATE TABLE native_helm_publications (
 repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
 name TEXT NOT NULL,
 version TEXT NOT NULL,
 session_id UUID NOT NULL UNIQUE REFERENCES native_helm_publication_sessions(id),
 digest TEXT NOT NULL CHECK(digest ~ '^sha256:[0-9a-f]{64}$'),
 object_key TEXT NOT NULL,
 size BIGINT NOT NULL CHECK(size>0),
 provenance_digest TEXT NOT NULL DEFAULT '',
 provenance_key TEXT NOT NULL DEFAULT '',
 provenance_size BIGINT NOT NULL DEFAULT 0 CHECK(provenance_size>=0),
 metadata_json TEXT NOT NULL CHECK(jsonb_typeof(metadata_json::jsonb)='object'),
 chart_yaml BYTEA NOT NULL CHECK(octet_length(chart_yaml)>0),
 publisher TEXT NOT NULL,
 published_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(repository_id,name,version),
 CHECK((provenance_size=0 AND provenance_digest='' AND provenance_key='') OR
       (provenance_size>0 AND provenance_digest ~ '^sha256:[0-9a-f]{64}$' AND provenance_key<>''))
);

-- +goose Down
-- Forward-only: durable chart identities and verified bytes are not downgraded.
