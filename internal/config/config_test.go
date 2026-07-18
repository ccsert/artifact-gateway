package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsIncompleteConfiguration(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "")
	t.Setenv("GATEWAY_REDIS_ADDRESS", "")
	t.Setenv("GATEWAY_S3_ENDPOINT", "")
	t.Setenv("GATEWAY_S3_BUCKET", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadAcceptsTestAdapterWithoutGiteaCredentials(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_REDIS_ADDRESS", "redis:6379")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_AUTH_TOKEN", "test-token")
	t.Setenv("GATEWAY_ADAPTER_MODE", "test")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AdapterMode != "test" {
		t.Fatalf("AdapterMode = %q", cfg.AdapterMode)
	}
}

func TestLoadDoesNotIncludeDatabaseURLInValidationError(t *testing.T) {
	secret := "not-a-url-password"
	t.Setenv("GATEWAY_DATABASE_URL", secret)
	t.Setenv("GATEWAY_REDIS_ADDRESS", "redis:6379")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_AUTH_TOKEN", "test-token")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked database URL: %v", err)
	}
}
