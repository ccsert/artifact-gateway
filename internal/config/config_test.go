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
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_ADAPTER_MODE", "test")
	t.Setenv("GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS", "repo.example, mirror.example ")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AdapterMode != "test" {
		t.Fatalf("AdapterMode = %q", cfg.AdapterMode)
	}
	if got := strings.Join(cfg.MavenProxyAllowedHosts, ","); got != "repo.example,mirror.example" {
		t.Fatalf("MavenProxyAllowedHosts = %q", got)
	}
}

func TestLoadRequiresGiteaCredentialsForGiteaAdapter(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_REDIS_ADDRESS", "redis:6379")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_ADAPTER_MODE", "gitea")
	t.Setenv("GATEWAY_GITEA_USERNAME", "")
	t.Setenv("GATEWAY_GITEA_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadDoesNotIncludeDatabaseURLInValidationError(t *testing.T) {
	secret := "not-a-url-password"
	t.Setenv("GATEWAY_DATABASE_URL", secret)
	t.Setenv("GATEWAY_REDIS_ADDRESS", "redis:6379")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked database URL: %v", err)
	}
}

func TestRepositoryReadersParsesActorRepositoryPatterns(t *testing.T) {
	readers := repositoryReaders("ci=team/*|shared/base; release = releases/*")
	if got := strings.Join(readers["ci"], ","); got != "team/*,shared/base" {
		t.Fatalf("ci readers = %q", got)
	}
	if got := strings.Join(readers["release"], ","); got != "releases/*" {
		t.Fatalf("release readers = %q", got)
	}
}
