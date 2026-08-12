-- +goose Up
CREATE TABLE IF NOT EXISTS native_apt_publication_sessions (
    id UUID PRIMARY KEY,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    suite TEXT NOT NULL CHECK (suite ~ '^[a-z0-9][a-z0-9+.-]{0,127}$'),
    component TEXT NOT NULL CHECK (component ~ '^[a-z0-9][a-z0-9+.-]{0,127}$'),
    publisher TEXT NOT NULL CHECK (publisher <> '' AND length(publisher) <= 512),
    object_name TEXT NOT NULL CHECK (object_name <> '' AND length(object_name) <= 255 AND object_name !~ '/' AND object_name LIKE '%.deb'),
    declared_digest TEXT NOT NULL CHECK (declared_digest ~ '^sha256:[0-9a-f]{64}$'),
    declared_size BIGINT NOT NULL CHECK (declared_size > 0 AND declared_size <= 1073741824),
    expected_identity TEXT NOT NULL DEFAULT '' CHECK (length(expected_identity) <= 1024),
    object_key TEXT NOT NULL DEFAULT '',
    package_revision_id UUID,
    state TEXT NOT NULL CHECK (state IN ('open','uploading','staged','aborted')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((state='open' AND object_key='' AND package_revision_id IS NULL)
        OR (state='uploading' AND object_key=('native/apt/sha256/' || substr(declared_digest,8)) AND package_revision_id IS NULL)
        OR (state='staged' AND object_key=('native/apt/sha256/' || substr(declared_digest,8)) AND package_revision_id IS NOT NULL)
        OR state='aborted')
);
CREATE INDEX IF NOT EXISTS native_apt_publication_sessions_expiry_idx
    ON native_apt_publication_sessions (expires_at, id)
    WHERE state IN ('open','uploading');

CREATE TABLE IF NOT EXISTS native_apt_package_revisions (
    id UUID PRIMARY KEY,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    package_name TEXT NOT NULL CHECK (package_name ~ '^[a-z0-9][a-z0-9+.-]+$'),
    version TEXT NOT NULL CHECK (version ~ '^([0-9]+:)?[0-9][A-Za-z0-9.+:~-]*$' AND length(version) <= 255),
    architecture TEXT NOT NULL CHECK (architecture ~ '^[a-z0-9][a-z0-9-]*$'),
    canonical_identity TEXT NOT NULL CHECK (canonical_identity = package_name || '@' || version || '#' || architecture AND length(canonical_identity) <= 1024),
    digest TEXT NOT NULL CHECK (digest ~ '^sha256:[0-9a-f]{64}$'),
    object_key TEXT NOT NULL CHECK (object_key = 'native/apt/sha256/' || substr(digest,8)),
    size BIGINT NOT NULL CHECK (size > 0 AND size <= 1073741824),
    object_name TEXT NOT NULL CHECK (object_name <> '' AND length(object_name) <= 255 AND object_name !~ '/' AND object_name LIKE '%.deb'),
    publisher TEXT NOT NULL CHECK (publisher <> '' AND length(publisher) <= 512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (repository_id, canonical_identity)
);
CREATE INDEX IF NOT EXISTS native_apt_package_revisions_digest_idx
    ON native_apt_package_revisions (repository_id, digest);
CREATE INDEX IF NOT EXISTS native_apt_package_revisions_object_idx
    ON native_apt_package_revisions (object_key);

ALTER TABLE native_apt_publication_sessions
    ADD CONSTRAINT native_apt_publication_sessions_package_revision_fk
    FOREIGN KEY (package_revision_id) REFERENCES native_apt_package_revisions(id);

CREATE TABLE IF NOT EXISTS native_apt_publication_idempotency (
    actor TEXT NOT NULL,
    target TEXT NOT NULL,
    key TEXT NOT NULL,
    payload_hash TEXT NOT NULL,
    session_id UUID NOT NULL REFERENCES native_apt_publication_sessions(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (actor, target, key)
);
CREATE INDEX IF NOT EXISTS native_apt_publication_idempotency_expiry_idx
    ON native_apt_publication_idempotency (expires_at);

CREATE TABLE IF NOT EXISTS native_apt_repository_snapshots (
    id UUID PRIMARY KEY,
    repository_id UUID NOT NULL REFERENCES hosted_repositories(id) ON DELETE CASCADE,
    suite TEXT NOT NULL CHECK (suite ~ '^[a-z0-9][a-z0-9+.-]{0,127}$'),
    sequence BIGINT NOT NULL CHECK (sequence > 0),
    state TEXT NOT NULL CHECK (state IN ('building','signed','visible','retired','failed')),
    release_digest TEXT NOT NULL DEFAULT '' CHECK (release_digest='' OR release_digest ~ '^sha256:[0-9a-f]{64}$'),
    inrelease_digest TEXT NOT NULL DEFAULT '' CHECK (inrelease_digest='' OR inrelease_digest ~ '^sha256:[0-9a-f]{64}$'),
    signer_identity TEXT NOT NULL DEFAULT '',
    key_fingerprint TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    UNIQUE (repository_id, suite, sequence),
    CHECK ((state IN ('building','failed') AND published_at IS NULL)
        OR (state='signed' AND published_at IS NULL)
        OR (state IN ('visible','retired') AND published_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS native_apt_repository_snapshots_visible_idx
    ON native_apt_repository_snapshots (repository_id, suite)
    WHERE state='visible';

CREATE TABLE IF NOT EXISTS native_apt_snapshot_packages (
    snapshot_id UUID NOT NULL REFERENCES native_apt_repository_snapshots(id) ON DELETE CASCADE,
    publication_session_id UUID NOT NULL REFERENCES native_apt_publication_sessions(id),
    package_revision_id UUID NOT NULL REFERENCES native_apt_package_revisions(id),
    component TEXT NOT NULL CHECK (component ~ '^[a-z0-9][a-z0-9+.-]{0,127}$'),
    architecture TEXT NOT NULL CHECK (architecture ~ '^[a-z0-9][a-z0-9-]*$'),
    PRIMARY KEY (snapshot_id, publication_session_id),
    UNIQUE (snapshot_id, package_revision_id, component, architecture)
);
CREATE INDEX IF NOT EXISTS native_apt_snapshot_packages_component_idx
    ON native_apt_snapshot_packages (snapshot_id, component, architecture);

-- +goose Down
DROP TABLE IF EXISTS native_apt_snapshot_packages;
DROP TABLE IF EXISTS native_apt_repository_snapshots;
DROP TABLE IF EXISTS native_apt_publication_idempotency;
ALTER TABLE native_apt_publication_sessions
    DROP CONSTRAINT IF EXISTS native_apt_publication_sessions_package_revision_fk;
DROP TABLE IF EXISTS native_apt_package_revisions;
DROP TABLE IF EXISTS native_apt_publication_sessions;
