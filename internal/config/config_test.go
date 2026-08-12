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
	t.Setenv("GATEWAY_NPM_METADATA_CACHE_TTL", "2m")
	t.Setenv("GATEWAY_NPM_NEGATIVE_CACHE_TTL", "45s")
	t.Setenv("GATEWAY_NPM_PROXY_BREAKER_TTL", "12s")
	t.Setenv("GATEWAY_LOCAL_AUTH_MAX_FAILED_ATTEMPTS", "7")
	t.Setenv("GATEWAY_LOCAL_AUTH_LOCKOUT_DURATION", "20m")
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
	if cfg.NPMMetadataCacheTTL != 2*time.Minute || cfg.NPMNegativeCacheTTL != 45*time.Second || cfg.NPMProxyBreakerTTL != 12*time.Second {
		t.Fatalf("npm proxy TTLs = metadata %s negative %s breaker %s", cfg.NPMMetadataCacheTTL, cfg.NPMNegativeCacheTTL, cfg.NPMProxyBreakerTTL)
	}
	if cfg.LocalAuthMaxFailedAttempts != 7 || cfg.LocalAuthLockoutDuration != 20*time.Minute {
		t.Fatalf("local auth policy = attempts %d lockout %s", cfg.LocalAuthMaxFailedAttempts, cfg.LocalAuthLockoutDuration)
	}
	if !cfg.HasRole(NodeRoleAPI) || !cfg.HasRole(NodeRoleScheduler) || !cfg.HasRole(NodeRoleWorker) {
		t.Fatalf("default node roles = %#v", cfg.NodeRoles)
	}
	if cfg.InstanceID == "" || !cfg.WorkerEnabled("maven", "promotion") {
		t.Fatalf("runtime config = instance %q roles %#v formats %#v kinds %#v", cfg.InstanceID, cfg.NodeRoles, cfg.WorkerFormats, cfg.WorkerKinds)
	}
	if cfg.RuntimeNodeRetention != 7*24*time.Hour || cfg.RuntimeNodePruneInterval != time.Hour {
		t.Fatalf("runtime node cleanup defaults = retention %s interval %s", cfg.RuntimeNodeRetention, cfg.RuntimeNodePruneInterval)
	}
}

func TestLoadRejectsInvalidLocalAuthenticationPolicy(t *testing.T) {
	for name, value := range map[string]string{
		"GATEWAY_LOCAL_AUTH_MAX_FAILED_ATTEMPTS": "0",
		"GATEWAY_LOCAL_AUTH_LOCKOUT_DURATION":    "30s",
	} {
		setCompleteConfiguration(t)
		t.Setenv(name, value)
		if _, err := Load(); err == nil {
			t.Fatalf("Load() accepted %s=%q", name, value)
		}
		t.Setenv(name, "")
	}
}

func TestLoadParsesClusterNodeRolesAndWorkerFilters(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_NODE_ROLES", "WORKER,worker")
	t.Setenv("GATEWAY_INSTANCE_ID", "oci-worker-01")
	t.Setenv("GATEWAY_WORKER_FORMATS", "OCI, raw,oci")
	t.Setenv("GATEWAY_WORKER_KINDS", "Replication,reclaim,replication")
	t.Setenv("GATEWAY_RUNTIME_NODE_RETENTION", "48h")
	t.Setenv("GATEWAY_RUNTIME_NODE_PRUNE_INTERVAL", "900")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.InstanceID != "oci-worker-01" || cfg.HasRole(NodeRoleAPI) || !cfg.HasRole(NodeRoleWorker) || len(cfg.NodeRoles) != 1 {
		t.Fatalf("cluster roles = %#v instance=%q", cfg.NodeRoles, cfg.InstanceID)
	}
	if len(cfg.WorkerFormats) != 2 || len(cfg.WorkerKinds) != 2 || !cfg.WorkerEnabled("oci", "replication") || !cfg.WorkerEnabled("raw", "reclaim") || cfg.WorkerEnabled("maven", "reclaim") || cfg.WorkerEnabled("oci", "promotion") {
		t.Fatalf("worker filters formats=%#v kinds=%#v", cfg.WorkerFormats, cfg.WorkerKinds)
	}
	if cfg.RuntimeNodeRetention != 48*time.Hour || cfg.RuntimeNodePruneInterval != 15*time.Minute {
		t.Fatalf("runtime node cleanup = retention %s interval %s", cfg.RuntimeNodeRetention, cfg.RuntimeNodePruneInterval)
	}
}

func TestLoadSupportsDedicatedIntelligenceWorker(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_NODE_ROLES", "worker")
	t.Setenv("GATEWAY_WORKER_FORMATS", "maven,oci")
	t.Setenv("GATEWAY_WORKER_KINDS", "intelligence")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.WorkerKinds) != 1 || !cfg.WorkerEnabled("maven", "intelligence") || !cfg.WorkerEnabled("oci", "intelligence") {
		t.Fatalf("dedicated intelligence worker filters formats=%#v kinds=%#v", cfg.WorkerFormats, cfg.WorkerKinds)
	}
	if cfg.WorkerEnabled("maven", "promotion") || cfg.WorkerEnabled("raw", "intelligence") {
		t.Fatalf("dedicated intelligence worker admitted an excluded route: formats=%#v kinds=%#v", cfg.WorkerFormats, cfg.WorkerKinds)
	}
}

func TestLoadSupportsDedicatedWebhookWorker(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_NODE_ROLES", "worker")
	t.Setenv("GATEWAY_WORKER_FORMATS", "oci")
	t.Setenv("GATEWAY_WORKER_KINDS", "webhook")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HasRole(NodeRoleWorker) || !cfg.WorkerKindEnabled("webhook") || cfg.WorkerKindEnabled("replication") {
		t.Fatalf("dedicated webhook worker roles=%#v formats=%#v kinds=%#v", cfg.NodeRoles, cfg.WorkerFormats, cfg.WorkerKinds)
	}
}

func TestLoadConfiguresArtifactScanner(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_SCANNER_ENDPOINT", "http://127.0.0.1:18082/v1/scan")
	t.Setenv("GATEWAY_SCANNER_HEALTH_ENDPOINT", "http://127.0.0.1:18082/v1/health")
	t.Setenv("GATEWAY_SCANNER_NAME", "trivy")
	t.Setenv("GATEWAY_SCANNER_TOKEN", "scanner-token")
	t.Setenv("GATEWAY_SCANNER_TIMEOUT", "90s")
	t.Setenv("GATEWAY_SCANNER_HEALTH_TIMEOUT", "3s")
	t.Setenv("GATEWAY_SCANNER_DATABASE_MAX_AGE", "12h")
	t.Setenv("GATEWAY_SCANNER_MAX_RESPONSE_BYTES", "4096")
	t.Setenv("GATEWAY_SCANNER_MAX_ARTIFACT_BYTES", "1000000")
	t.Setenv("GATEWAY_SCANNER_FORMATS", "OCI,maven,go,oci")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.ScannerEnabled() || cfg.ScannerHealthEndpoint != "http://127.0.0.1:18082/v1/health" || cfg.ScannerName != "trivy" || cfg.ScannerToken != "scanner-token" || cfg.ScannerTimeout != 90*time.Second || cfg.ScannerHealthTimeout != 3*time.Second || cfg.ScannerDatabaseMaxAge != 12*time.Hour || cfg.ScannerMaxResponseBytes != 4096 || cfg.ScannerMaxArtifactBytes != 1000000 {
		t.Fatalf("scanner config enabled=%t name=%q timeout=%s health_timeout=%s database_max_age=%s response_limit=%d artifact_limit=%d", cfg.ScannerEnabled(), cfg.ScannerName, cfg.ScannerTimeout, cfg.ScannerHealthTimeout, cfg.ScannerDatabaseMaxAge, cfg.ScannerMaxResponseBytes, cfg.ScannerMaxArtifactBytes)
	}
	if len(cfg.ScannerFormats) != 3 || !cfg.ScannerFormatEnabled("oci") || !cfg.ScannerFormatEnabled("maven") || !cfg.ScannerFormatEnabled("go") || cfg.ScannerFormatEnabled("raw") {
		t.Fatalf("scanner formats = %#v", cfg.ScannerFormats)
	}
}

func TestLoadRejectsInvalidArtifactScannerConfiguration(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{name: "insecure endpoint", value: "http://scanner.example.test/v1/scan"},
		{name: "endpoint query", value: "https://scanner.example.test/v1/scan?token=secret"},
		{name: "insecure health endpoint", value: "http://scanner.example.test/health"},
		{name: "unknown format", value: "maven,unknown"},
		{name: "short timeout", value: "500ms"},
		{name: "long health timeout", value: "31s"},
		{name: "short database max age", value: "30s"},
		{name: "small response limit", value: "512"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			setCompleteConfiguration(t)
			switch testCase.name {
			case "insecure health endpoint":
				t.Setenv("GATEWAY_SCANNER_ENDPOINT", "https://scanner.example.test/v1/scan")
				t.Setenv("GATEWAY_SCANNER_HEALTH_ENDPOINT", testCase.value)
			case "unknown format":
				t.Setenv("GATEWAY_SCANNER_ENDPOINT", "https://scanner.example.test/v1/scan")
				t.Setenv("GATEWAY_SCANNER_FORMATS", testCase.value)
			case "short timeout":
				t.Setenv("GATEWAY_SCANNER_ENDPOINT", "https://scanner.example.test/v1/scan")
				t.Setenv("GATEWAY_SCANNER_TIMEOUT", testCase.value)
			case "long health timeout":
				t.Setenv("GATEWAY_SCANNER_ENDPOINT", "https://scanner.example.test/v1/scan")
				t.Setenv("GATEWAY_SCANNER_HEALTH_TIMEOUT", testCase.value)
			case "short database max age":
				t.Setenv("GATEWAY_SCANNER_ENDPOINT", "https://scanner.example.test/v1/scan")
				t.Setenv("GATEWAY_SCANNER_DATABASE_MAX_AGE", testCase.value)
			case "small response limit":
				t.Setenv("GATEWAY_SCANNER_ENDPOINT", "https://scanner.example.test/v1/scan")
				t.Setenv("GATEWAY_SCANNER_MAX_RESPONSE_BYTES", testCase.value)
			default:
				t.Setenv("GATEWAY_SCANNER_ENDPOINT", testCase.value)
			}
			if _, err := Load(); err == nil {
				t.Fatalf("Load() accepted invalid scanner configuration")
			}
		})
	}
}

func TestLoadRejectsScannerSettingsWithoutEndpoint(t *testing.T) {
	setCompleteConfiguration(t)
	secret := "scanner-secret-that-must-not-be-logged"
	t.Setenv("GATEWAY_SCANNER_TOKEN", secret)
	if _, err := Load(); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("Load() error=%v", err)
	}
}

func TestLoadRejectsUnknownClusterRoleOrWorkerFilter(t *testing.T) {
	setCompleteConfiguration(t)
	for name, value := range map[string]string{
		"GATEWAY_NODE_ROLES":     "worker,unknown",
		"GATEWAY_WORKER_FORMATS": "oci,unknown",
		"GATEWAY_WORKER_KINDS":   "replication,unknown",
	} {
		t.Setenv(name, value)
		if _, err := Load(); err == nil {
			t.Fatalf("Load() accepted %s=%q", name, value)
		}
		t.Setenv(name, "")
	}
}

func TestLoadRejectsStandaloneRoleCombination(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_NODE_ROLES", "standalone,worker")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted standalone role combination")
	}
}

func setCompleteConfiguration(t *testing.T) {
	t.Helper()
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
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

func TestLoadAllowsLoopbackHTTPForLocalOIDCTestProvider(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_OIDC_ISSUER", "http://127.0.0.1:18081/realms/artifact-gateway")
	t.Setenv("GATEWAY_OIDC_AUDIENCE", "artifact-gateway-api")
	t.Setenv("GATEWAY_OIDC_JWKS_URL", "http://127.0.0.1:18081/realms/artifact-gateway/protocol/openid-connect/certs")
	t.Setenv("GATEWAY_OIDC_CLIENT_ID", "artifact-gateway-console")
	t.Setenv("GATEWAY_OIDC_REDIRECT_URL", "http://127.0.0.1:4173/auth/oidc/callback")

	if _, err := Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadRejectsNonLoopbackHTTPOIDCIssuer(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_OIDC_ISSUER", "http://keycloak.example.test/realms/artifact-gateway")
	t.Setenv("GATEWAY_OIDC_AUDIENCE", "artifact-gateway-api")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a non-loopback HTTP OIDC issuer")
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

func TestLoadConfiguresOIDCBrowserLogin(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_OIDC_ISSUER", "https://login.example.test")
	t.Setenv("GATEWAY_OIDC_AUDIENCE", "artifact-gateway")
	t.Setenv("GATEWAY_OIDC_CLIENT_ID", "artifact-gateway-console")
	t.Setenv("GATEWAY_OIDC_CLIENT_SECRET", "client-secret")
	t.Setenv("GATEWAY_OIDC_REDIRECT_URL", "https://gateway.example.test/auth/oidc/callback")
	t.Setenv("GATEWAY_OIDC_SCOPES", "openid profile,groups profile")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDCClientID != "artifact-gateway-console" || cfg.OIDCClientSecret != "client-secret" || cfg.OIDCRedirectURL != "https://gateway.example.test/auth/oidc/callback" {
		t.Fatalf("OIDC browser config = %#v", cfg)
	}
	if got := strings.Join(cfg.OIDCScopes, ","); got != "openid,profile,groups" {
		t.Fatalf("OIDC scopes = %q", got)
	}
}

func TestLoadRejectsInsecureOIDCBrowserRedirect(t *testing.T) {
	setCompleteConfiguration(t)
	t.Setenv("GATEWAY_OIDC_ISSUER", "https://login.example.test")
	t.Setenv("GATEWAY_OIDC_AUDIENCE", "artifact-gateway")
	t.Setenv("GATEWAY_OIDC_CLIENT_ID", "artifact-gateway-console")
	t.Setenv("GATEWAY_OIDC_REDIRECT_URL", "http://gateway.example.test/auth/oidc/callback")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a non-local HTTP OIDC redirect")
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
	t.Setenv("GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_OPEN_CONNS", "3")
	t.Setenv("GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_IDLE_CONNS", "1")
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
	if cfg.DatabaseArtifactLockPool.MaxOpenConns != 3 || cfg.DatabaseArtifactLockPool.MaxIdleConns != 1 || cfg.DatabaseArtifactLockPool.ConnMaxLifetime != 20*time.Minute || cfg.DatabaseArtifactLockPool.ConnMaxIdleTime != 2*time.Minute {
		t.Fatalf("database artifact lock pool=%+v", cfg.DatabaseArtifactLockPool)
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

func TestLoadRejectsArtifactLockIdleConnectionsAboveOpenLimit(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "postgres://gateway:password@db:5432/gateway")
	t.Setenv("GATEWAY_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("GATEWAY_S3_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_S3_ACCESS_KEY", "minio-user")
	t.Setenv("GATEWAY_S3_SECRET_KEY", "minio-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")
	t.Setenv("GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_OPEN_CONNS", "1")
	t.Setenv("GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_IDLE_CONNS", "2")
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want database artifact lock pool validation error")
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
