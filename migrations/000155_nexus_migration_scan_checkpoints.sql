-- Scan receipts remain private and non-executable until the entire inventory is
-- enumerated, re-observed and sealed by the ordinary manifest transaction.
CREATE TABLE nexus_migration_scans (
    batch_id uuid PRIMARY KEY REFERENCES nexus_migration_batches(id) ON DELETE CASCADE,
    binding_sha256 text NOT NULL CHECK(binding_sha256 ~ '^[0-9a-f]{64}$'),
    repositories jsonb NOT NULL CHECK(jsonb_typeof(repositories)='array'),
    repository_index integer NOT NULL DEFAULT 0 CHECK(repository_index>=0),
    cursor text NOT NULL DEFAULT '' CHECK(length(cursor)<=4096),
    page_count bigint NOT NULL DEFAULT 0 CHECK(page_count>=0),
    verified_pages bigint NOT NULL DEFAULT 0 CHECK(verified_pages>=0 AND verified_pages<=page_count),
    asset_count bigint NOT NULL DEFAULT 0 CHECK(asset_count>=0),
    metadata_bytes bigint NOT NULL DEFAULT 0 CHECK(metadata_bytes>=0)
);
CREATE TABLE nexus_migration_scan_pages (
    batch_id uuid NOT NULL REFERENCES nexus_migration_scans(batch_id) ON DELETE CASCADE,
    ordinal bigint NOT NULL CHECK(ordinal>=0),
    source_repository text NOT NULL,
    cursor text NOT NULL CHECK(length(cursor)<=4096),
    cursor_sha256 text NOT NULL CHECK(cursor_sha256 ~ '^[0-9a-f]{64}$'),
    page_sha256 text NOT NULL CHECK(page_sha256 ~ '^[0-9a-f]{64}$'),
    page jsonb NOT NULL CHECK(jsonb_typeof(page)='object'),
    PRIMARY KEY(batch_id,ordinal),
    UNIQUE(batch_id,source_repository,cursor_sha256)
);
CREATE TABLE nexus_migration_scan_paths (
    batch_id uuid NOT NULL REFERENCES nexus_migration_scans(batch_id) ON DELETE CASCADE,
    source_repository text NOT NULL,
    source_path text NOT NULL,
    PRIMARY KEY(batch_id,source_repository,source_path)
);
