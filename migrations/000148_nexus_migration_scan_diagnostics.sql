-- Diagnostic evidence is independent of the sealed executable manifest.
ALTER TABLE nexus_migration_batches
    ADD COLUMN last_scan_failure jsonb
    CHECK(last_scan_failure IS NULL OR jsonb_typeof(last_scan_failure)='object');
