package config

import "testing"

func TestMigrationPoolIsExplicitOptionalAndWorkerKindIsFilterable(t *testing.T) {
	for key, value := range map[string]string{"GATEWAY_DATABASE_URL": "postgres://gateway:test@localhost/gateway_test", "GATEWAY_RUSTFS_ENDPOINT": "http://rustfs:9000", "GATEWAY_RUSTFS_BUCKET": "test", "GATEWAY_RUSTFS_ACCESS_KEY": "test-key", "GATEWAY_RUSTFS_SECRET_KEY": "test-secret", "GATEWAY_ADMIN_TOKEN": "test-admin", "GATEWAY_RESOLVER_TOKEN": "test-resolver", "GATEWAY_MIGRATION_STORAGE_POOL_ID": "", "GATEWAY_WORKER_KINDS": "migration", "GATEWAY_WORKER_FORMATS": "raw"} {
		t.Setenv(key, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MigrationStoragePoolID != "" || !cfg.WorkerEnabled("raw", "migration") || cfg.WorkerEnabled("maven", "migration") || cfg.WorkerKindEnabled("replication") {
		t.Fatalf("migration runtime filters=%+v", cfg.WorkerKinds)
	}
	t.Setenv("GATEWAY_MIGRATION_STORAGE_POOL_ID", "owned.storage-pool:1")
	cfg, err = Load()
	if err != nil || cfg.MigrationStoragePoolID != "owned.storage-pool:1" {
		t.Fatalf("explicit pool=%q error=%v", cfg.MigrationStoragePoolID, err)
	}
	t.Setenv("GATEWAY_MIGRATION_STORAGE_POOL_ID", "secret/invalid-pool")
	if _, err = Load(); err == nil {
		t.Fatal("invalid opaque pool identity was accepted")
	}
}
