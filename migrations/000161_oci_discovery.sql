-- +goose Up
-- Immutable principal-bound enumeration, distinct from the content cache.
CREATE TABLE oci_discovery_snapshots (
    id UUID PRIMARY KEY,
    owner TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('catalog','referrers')),
    payload JSONB NOT NULL CHECK (octet_length(payload::text) <= 8388608),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX oci_discovery_snapshot_owner_expiry ON oci_discovery_snapshots(owner,expires_at);
CREATE TABLE oci_subject_bindings (
    scope TEXT NOT NULL,
    owner TEXT NOT NULL CHECK (owner ~ '^[a-f0-9]{64}$'),
    image TEXT NOT NULL,
    subject TEXT NOT NULL CHECK (subject ~ '^sha256:[a-f0-9]{64}$'),
    source_id UUID NOT NULL REFERENCES hosted_repositories(id),
    source_version BIGINT NOT NULL CHECK (source_version > 0),
    payload JSONB NOT NULL CHECK (octet_length(payload::text) <= 8388608),
    PRIMARY KEY (scope,owner,image,subject)
);
-- One digest within a view/image cannot acquire another source through a
-- second subject. Metadata owns references to ordinary quota-managed cache
-- objects; expired/missing bytes fail closed and are re-fetched only there.
CREATE TABLE oci_bound_objects (
    scope TEXT NOT NULL,
    owner TEXT NOT NULL CHECK (owner ~ '^[a-f0-9]{64}$'),
    image TEXT NOT NULL,
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[a-f0-9]{64}$'),
    subject TEXT NOT NULL,
    source_id UUID NOT NULL REFERENCES hosted_repositories(id),
    source_version BIGINT NOT NULL CHECK (source_version > 0),
    payload JSONB NOT NULL,
    PRIMARY KEY (scope,owner,image,digest),
    FOREIGN KEY (scope,owner,image,subject) REFERENCES oci_subject_bindings(scope,owner,image,subject)
);
-- +goose Down
-- Forward-only; compensate with a new migration.
