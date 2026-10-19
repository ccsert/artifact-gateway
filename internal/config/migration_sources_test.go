package config

import "testing"

func TestMigrationSourcePrivateEndpointsRequireOperatorExactHostAndPrivateCIDR(t *testing.T) {
	for key, value := range map[string]string{"GATEWAY_DATABASE_URL": "postgres://gateway:test@localhost/gateway_test", "GATEWAY_RUSTFS_ENDPOINT": "http://rustfs:9000", "GATEWAY_RUSTFS_BUCKET": "test", "GATEWAY_RUSTFS_ACCESS_KEY": "test-key", "GATEWAY_RUSTFS_SECRET_KEY": "test-secret", "GATEWAY_ADMIN_TOKEN": "test-admin", "GATEWAY_RESOLVER_TOKEN": "test-resolver"} {
		t.Setenv(key, value)
	}
	t.Setenv("GATEWAY_MIGRATION_SOURCE_PRIVATE_ENDPOINTS", "nexus.internal=10.20.30.0/24,nexus.internal=192.168.10.0/24,10.0.0.7=10.0.0.7/32")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.MigrationSourcePrivateEndpoints["nexus.internal"]) != 2 || len(cfg.MigrationSourcePrivateEndpoints["10.0.0.7"]) != 1 {
		t.Fatalf("policy=%v", cfg.MigrationSourcePrivateEndpoints)
	}
	for _, raw := range []string{"*=10.0.0.0/8", "nexus.internal=0.0.0.0/0", "nexus.internal=10.0.0.0/7", "nexus.internal=127.0.0.1/32", "nexus.internal=169.254.169.254/32", "https://nexus.internal=10.0.0.0/8", "nexus.internal:443=10.0.0.0/8", "nexus.internal=invalid"} {
		t.Setenv("GATEWAY_MIGRATION_SOURCE_PRIVATE_ENDPOINTS", raw)
		if _, err := Load(); err == nil {
			t.Fatalf("unsafe rule accepted: %s", raw)
		}
	}
}
