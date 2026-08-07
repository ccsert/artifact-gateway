package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRejectsIncompleteConfiguration(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "")
	t.Setenv("GATEWAY_S3_ENDPOINT", "")
	t.Setenv("GATEWAY_S3_BUCKET", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadAcceptsNativeConfiguration(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS", "repo.example, mirror.example ")
	t.Setenv("GATEWAY_RAW_PROXY_ALLOWED_HOSTS", "raw.example, mirror.example ")
	t.Setenv("GATEWAY_RAW_CACHE_MAX_OBJECT_BYTES", "12345")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := strings.Join(cfg.MavenProxyAllowedHosts, ","); got != "repo.example,mirror.example" {
		t.Fatalf("MavenProxyAllowedHosts = %q", got)
	}
	if got := strings.Join(cfg.RawProxyAllowedHosts, ","); got != "raw.example,mirror.example" {
		t.Fatalf("RawProxyAllowedHosts = %q", got)
	}
	if cfg.RawCacheMaxObjectBytes != 12345 {
		t.Fatalf("RawCacheMaxObjectBytes = %d", cfg.RawCacheMaxObjectBytes)
	}
}

func TestLoadDoesNotIncludeDatabaseURLInValidationError(t *testing.T) {
	secret := "not-a-url-password"
	t.Setenv("GATEWAY_DATABASE_URL", secret)
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

func TestRepositoryCacheQuotasParsesPositiveByteLimits(t *testing.T) {
	quotas := repositoryCacheQuotas("team/app=1024; engineering=2048; broken=no; zero=0")
	if quotas["team/app"] != 1024 || quotas["engineering"] != 2048 || len(quotas) != 2 {
		t.Fatalf("quotas = %#v", quotas)
	}
}

func TestLoadConfiguresOIDCWithHTTPSIssuerAndDiscoveryDefault(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_OIDC_ISSUER", "https://login.example.test/")
	t.Setenv("GATEWAY_OIDC_AUDIENCE", "artifact-gateway")
	t.Setenv("GATEWAY_OIDC_JWKS_URL", "")
	t.Setenv("GATEWAY_OIDC_ADMIN_SUBJECTS", "ops-admin, release-admin ")
	t.Setenv("GATEWAY_OIDC_READER_ROLES", "artifact-reader")
	t.Setenv("GATEWAY_OIDC_WRITER_ROLES", "artifact-writer")
	t.Setenv("GATEWAY_OIDC_ADMIN_ROLES", "artifact-admin")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDCIssuer != "https://login.example.test" || cfg.OIDCJWKSURL != "" || strings.Join(cfg.OIDCAdminSubjects, ",") != "ops-admin,release-admin" || strings.Join(cfg.OIDCRoles.Reader, ",") != "artifact-reader" || strings.Join(cfg.OIDCRoles.Writer, ",") != "artifact-writer" || strings.Join(cfg.OIDCRoles.Admin, ",") != "artifact-admin" {
		t.Fatalf("OIDC config = %#v", cfg)
	}
}

func TestLoadRejectsOIDCWithoutAudience(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_OIDC_ISSUER", "https://login.example.test")
	t.Setenv("GATEWAY_OIDC_AUDIENCE", "")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error")
	}
}

func TestLoadRejectsInvalidOTELSamplingRatio(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	for _, ratio := range []string{"1.1", "NaN"} {
		t.Setenv("GATEWAY_OTEL_SAMPLING_RATIO", ratio)
		if _, err := Load(); err == nil {
			t.Fatalf("Load() error = nil for sampling ratio %q", ratio)
		}
	}
}

func TestLoadParsesCacheTTLs(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_OCI_CACHE_TTL", "30m")
	t.Setenv("GATEWAY_MAVEN_CACHE_TTL", "900")
	t.Setenv("GATEWAY_MAVEN_METADATA_CACHE_TTL", "20m")
	t.Setenv("GATEWAY_MAVEN_NEGATIVE_CACHE_TTL", "120")
	t.Setenv("GATEWAY_RAW_CACHE_TTL", "1h30m")
	t.Setenv("GATEWAY_CONAN_CACHE_TTL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.OCICacheTTL != 30*time.Minute {
		t.Fatalf("OCICacheTTL = %v", cfg.OCICacheTTL)
	}
	if cfg.MavenCacheTTL != 15*time.Minute {
		t.Fatalf("MavenCacheTTL = %v", cfg.MavenCacheTTL)
	}
	if cfg.MavenMetadataCacheTTL != 20*time.Minute {
		t.Fatalf("MavenMetadataCacheTTL = %v", cfg.MavenMetadataCacheTTL)
	}
	if cfg.MavenNegativeCacheTTL != 2*time.Minute {
		t.Fatalf("MavenNegativeCacheTTL = %v", cfg.MavenNegativeCacheTTL)
	}
	if cfg.RawCacheTTL != 90*time.Minute {
		t.Fatalf("RawCacheTTL = %v", cfg.RawCacheTTL)
	}
	if cfg.ConanCacheTTL != 15*time.Minute {
		t.Fatalf("ConanCacheTTL = %v", cfg.ConanCacheTTL)
	}
}

func TestLoadUsesMavenCacheDefaultsSuitedToImmutableComponents(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_MAVEN_CACHE_TTL", "")
	t.Setenv("GATEWAY_MAVEN_METADATA_CACHE_TTL", "")
	t.Setenv("GATEWAY_MAVEN_NEGATIVE_CACHE_TTL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MavenCacheTTL != 24*time.Hour || cfg.MavenMetadataCacheTTL != 15*time.Minute || cfg.MavenNegativeCacheTTL != 10*time.Minute {
		t.Fatalf("Maven cache TTLs = component:%v metadata:%v negative:%v", cfg.MavenCacheTTL, cfg.MavenMetadataCacheTTL, cfg.MavenNegativeCacheTTL)
	}
}

func TestLoadConfiguresDatabasePool(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_DATABASE_MAX_OPEN_CONNS", "24")
	t.Setenv("GATEWAY_DATABASE_MAX_IDLE_CONNS", "6")
	t.Setenv("GATEWAY_DATABASE_COORDINATOR_MAX_OPEN_CONNS", "5")
	t.Setenv("GATEWAY_DATABASE_COORDINATOR_MAX_IDLE_CONNS", "1")
	t.Setenv("GATEWAY_DATABASE_CONN_MAX_LIFETIME", "20m")
	t.Setenv("GATEWAY_DATABASE_CONN_MAX_IDLE_TIME", "2m")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabasePool.MaxOpenConns != 24 || cfg.DatabasePool.MaxIdleConns != 6 || cfg.DatabasePool.ConnMaxLifetime != 20*time.Minute || cfg.DatabasePool.ConnMaxIdleTime != 2*time.Minute {
		t.Fatalf("database pool=%+v", cfg.DatabasePool)
	}
	if cfg.DatabaseCoordinatorPool.MaxOpenConns != 5 || cfg.DatabaseCoordinatorPool.MaxIdleConns != 1 || cfg.DatabaseCoordinatorPool.ConnMaxLifetime != 20*time.Minute || cfg.DatabaseCoordinatorPool.ConnMaxIdleTime != 2*time.Minute {
		t.Fatalf("database coordinator pool=%+v", cfg.DatabaseCoordinatorPool)
	}
}

func TestLoadRejectsDatabaseIdleConnectionsAboveOpenLimit(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_DATABASE_MAX_OPEN_CONNS", "2")
	t.Setenv("GATEWAY_DATABASE_MAX_IDLE_CONNS", "3")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want database pool validation error")
	}
}

func TestLoadRejectsCoordinatorIdleConnectionsAboveOpenLimit(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_DATABASE_COORDINATOR_MAX_OPEN_CONNS", "1")
	t.Setenv("GATEWAY_DATABASE_COORDINATOR_MAX_IDLE_CONNS", "2")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want database coordinator pool validation error")
	}
}

func TestLoadRejectsInvalidCacheTTL(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	for _, name := range []string{"GATEWAY_OCI_CACHE_TTL", "GATEWAY_MAVEN_CACHE_TTL", "GATEWAY_MAVEN_METADATA_CACHE_TTL", "GATEWAY_MAVEN_NEGATIVE_CACHE_TTL", "GATEWAY_RAW_CACHE_TTL", "GATEWAY_CONAN_CACHE_TTL"} {
		for _, value := range []string{"nope", "-5m", "0"} {
			t.Setenv(name, value)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() error = nil for %s=%q", name, value)
			}
		}
		t.Setenv(name, "")
	}
}
