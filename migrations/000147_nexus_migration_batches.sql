-- Durable migration manifests are independent of the native replication ledger.
CREATE TABLE nexus_migration_batches (
    id uuid PRIMARY KEY,
    actor text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    request jsonb NOT NULL,
    capacity_evidence jsonb NOT NULL,
    source_id uuid NOT NULL,
    source_version text NOT NULL,
    state text NOT NULL DEFAULT 'scanning' CHECK (state IN ('scanning','ready','running','pausing','paused','blocked','completed','failed','cancelled')),
    version bigint NOT NULL DEFAULT 1,
    manifest_sha256 text NOT NULL DEFAULT '',
    scan_complete boolean NOT NULL DEFAULT false,
    metadata_discovered bigint NOT NULL DEFAULT 0 CHECK (metadata_discovered>=0),
    source_verification_bytes bigint NOT NULL DEFAULT 0 CHECK(source_verification_bytes>=0),
    entry_count bigint NOT NULL DEFAULT 0 CHECK (entry_count>=0),
    total_bytes bigint CHECK (total_bytes>=0),
    lease_token text NOT NULL DEFAULT '',
    lease_epoch bigint NOT NULL DEFAULT 0,
    lease_expires_at timestamptz,
    pause_requested boolean NOT NULL DEFAULT false,
    cancel_requested boolean NOT NULL DEFAULT false,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(actor,idempotency_key)
);
CREATE INDEX nexus_migration_batches_claim ON nexus_migration_batches(state,created_at);
CREATE TABLE nexus_migration_entries (
    id uuid PRIMARY KEY,
    batch_id uuid NOT NULL REFERENCES nexus_migration_batches(id) ON DELETE CASCADE,
    ordinal integer NOT NULL CHECK(ordinal>=0),
    source_repository text NOT NULL,
    target_repository_id uuid NOT NULL,
    format text NOT NULL CHECK(format IN ('raw','maven')),
    identity text NOT NULL,
    state text NOT NULL CHECK(state IN ('pending','uploaded','verified','committed','skipped','conflict','failed','unsupported','source_missing','source_deleted')),
    assets jsonb NOT NULL,
    session_id text NOT NULL DEFAULT '',
    attempts integer NOT NULL DEFAULT 0 CHECK(attempts BETWEEN 0 AND 3),
    transferred_bytes bigint NOT NULL DEFAULT 0 CHECK(transferred_bytes>=0),
    verified_bytes bigint NOT NULL DEFAULT 0 CHECK(verified_bytes>=0),
    visible_identities bigint NOT NULL DEFAULT 0 CHECK(visible_identities>=0),
    last_error text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(batch_id,target_repository_id,identity),
    UNIQUE(batch_id,ordinal)
);
CREATE TABLE nexus_migration_controls (
    batch_id uuid NOT NULL REFERENCES nexus_migration_batches(id),
    actor text NOT NULL,
    idempotency_key text NOT NULL,
    operation text NOT NULL,
    request_hash text NOT NULL,
    response jsonb NOT NULL,
    PRIMARY KEY(batch_id,actor,idempotency_key)
);
