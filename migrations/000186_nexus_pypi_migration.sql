ALTER TABLE nexus_migration_entries DROP CONSTRAINT nexus_migration_entries_format_check;
ALTER TABLE nexus_migration_entries ADD CONSTRAINT nexus_migration_entries_format_check CHECK (format IN ('raw', 'maven', 'oci', 'conan', 'npm', 'pypi'));
