package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
	"github.com/artifact-gateway/artifact-gateway/internal/database"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

// NodeRole controls which parts of the Gateway runtime are enabled. The
// standalone role is the default and expands to every role for local use.
type NodeRole string

const (
	NodeRoleStandalone NodeRole = "standalone"
	NodeRoleAPI        NodeRole = "api"
	NodeRoleScheduler  NodeRole = "scheduler"
	NodeRoleWorker     NodeRole = "worker"
)

var supportedNodeRoles = map[NodeRole]struct{}{
	NodeRoleStandalone: {},
	NodeRoleAPI:        {},
	NodeRoleScheduler:  {},
	NodeRoleWorker:     {},
}

var supportedWorkerFormats = func() map[string]struct{} {
	formats := make(map[string]struct{})
	for _, format := range repository.WorkerFormats() {
		formats[string(format)] = struct{}{}
	}
	return formats
}()

var supportedScannerFormats = func() map[string]struct{} {
	formats := make(map[string]struct{})
	for _, format := range repository.SupportedFormats() {
		formats[string(format)] = struct{}{}
	}
	return formats
}()

var supportedWorkerKinds = map[string]struct{}{
	"promotion": {}, "replication": {}, "retention": {}, "reclaim": {},
	"intelligence": {}, "scan": {}, "deletion": {}, "recovery": {}, "cache": {}, "audit": {},
	"webhook": {},
}

type Config struct {
	ListenAddress              string
	NodeRoles                  []NodeRole
	InstanceID                 string
	WorkerFormats              []string
	WorkerKinds                []string
	ScannerEndpoint            string
	ScannerHealthEndpoint      string
	ScannerName                string
	ScannerToken               string
	ScannerTimeout             time.Duration
	ScannerHealthTimeout       time.Duration
	ScannerDatabaseMaxAge      time.Duration
	ScannerMaxResponseBytes    int64
	ScannerMaxArtifactBytes    int64
	ScannerFormats             []string
	RuntimeNodeRetention       time.Duration
	RuntimeNodePruneInterval   time.Duration
	DatabaseURL                string
	DatabasePool               database.PoolConfig
	DatabaseCoordinatorPool    database.PoolConfig
	DatabaseArtifactLockPool   database.PoolConfig
	S3Endpoint                 string
	S3Bucket                   string
	S3AccessKey                string
	S3SecretKey                string
	OCIProxyAllowedHosts       []string
	MavenProxyAllowedHosts     []string
	RawProxyAllowedHosts       []string
	RawCacheMaxObjectBytes     int64
	ConanCacheMaxObjectBytes   int64
	OCICacheTTL                time.Duration
	MavenCacheTTL              time.Duration
	MavenMetadataCacheTTL      time.Duration
	MavenNegativeCacheTTL      time.Duration
	NPMMetadataCacheTTL        time.Duration
	NPMNegativeCacheTTL        time.Duration
	NPMProxyBreakerTTL         time.Duration
	RawCacheTTL                time.Duration
	ConanCacheTTL              time.Duration
	AdminToken                 string
	ResolverToken              string
	AdminActor                 string
	ResolverActor              string
	LocalAuthMaxFailedAttempts int
	LocalAuthLockoutDuration   time.Duration
	RepositoryReaders          map[string][]string
	RepositoryCacheQuotas      map[string]int64
	OIDCIssuer                 string
	OIDCAudience               string
	OIDCJWKSURL                string
	OIDCClientID               string
	OIDCClientSecret           string
	OIDCRedirectURL            string
	OIDCScopes                 []string
	OIDCAdminSubjects          []string
	OIDCRoles                  authorization.OIDCRoleMapping
	OTLPHTTPEndpoint           string
	OTELSamplingRatio          float64
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:              value("GATEWAY_LISTEN_ADDRESS", ":8080"),
		NodeRoles:                  parseNodeRoles(os.Getenv("GATEWAY_NODE_ROLES")),
		InstanceID:                 value("GATEWAY_INSTANCE_ID", "gateway-"+hostname()),
		WorkerFormats:              parseFilter(os.Getenv("GATEWAY_WORKER_FORMATS"), supportedWorkerFormats),
		WorkerKinds:                parseFilter(os.Getenv("GATEWAY_WORKER_KINDS"), supportedWorkerKinds),
		ScannerEndpoint:            strings.TrimSpace(os.Getenv("GATEWAY_SCANNER_ENDPOINT")),
		ScannerHealthEndpoint:      strings.TrimSpace(os.Getenv("GATEWAY_SCANNER_HEALTH_ENDPOINT")),
		ScannerName:                strings.TrimSpace(os.Getenv("GATEWAY_SCANNER_NAME")),
		ScannerToken:               os.Getenv("GATEWAY_SCANNER_TOKEN"),
		ScannerTimeout:             2 * time.Minute,
		ScannerHealthTimeout:       2 * time.Second,
		ScannerDatabaseMaxAge:      24 * time.Hour,
		ScannerMaxResponseBytes:    512 << 10,
		ScannerMaxArtifactBytes:    20 << 30,
		RuntimeNodeRetention:       7 * 24 * time.Hour,
		RuntimeNodePruneInterval:   time.Hour,
		DatabaseURL:                os.Getenv("GATEWAY_DATABASE_URL"),
		DatabasePool:               database.DefaultPoolConfig(),
		DatabaseCoordinatorPool:    database.DefaultCoordinatorPoolConfig(),
		DatabaseArtifactLockPool:   database.DefaultArtifactLockPoolConfig(),
		S3Endpoint:                 os.Getenv("GATEWAY_S3_ENDPOINT"),
		S3Bucket:                   os.Getenv("GATEWAY_S3_BUCKET"),
		S3AccessKey:                os.Getenv("GATEWAY_S3_ACCESS_KEY"),
		S3SecretKey:                os.Getenv("GATEWAY_S3_SECRET_KEY"),
		OCIProxyAllowedHosts:       splitCSV(os.Getenv("GATEWAY_OCI_PROXY_ALLOWED_HOSTS")),
		MavenProxyAllowedHosts:     splitCSV(os.Getenv("GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS")),
		RawProxyAllowedHosts:       splitCSV(os.Getenv("GATEWAY_RAW_PROXY_ALLOWED_HOSTS")),
		RawCacheMaxObjectBytes:     1 << 30,
		ConanCacheMaxObjectBytes:   1 << 30,
		OCICacheTTL:                15 * time.Minute,
		MavenCacheTTL:              24 * time.Hour,
		MavenMetadataCacheTTL:      15 * time.Minute,
		MavenNegativeCacheTTL:      10 * time.Minute,
		NPMMetadataCacheTTL:        15 * time.Minute,
		NPMNegativeCacheTTL:        10 * time.Minute,
		NPMProxyBreakerTTL:         30 * time.Second,
		RawCacheTTL:                15 * time.Minute,
		ConanCacheTTL:              15 * time.Minute,
		AdminToken:                 os.Getenv("GATEWAY_ADMIN_TOKEN"),
		ResolverToken:              os.Getenv("GATEWAY_RESOLVER_TOKEN"),
		AdminActor:                 value("GATEWAY_ADMIN_ACTOR", "gateway-admin"),
		ResolverActor:              value("GATEWAY_RESOLVER_ACTOR", "gateway-resolver"),
		LocalAuthMaxFailedAttempts: 5,
		LocalAuthLockoutDuration:   15 * time.Minute,
		RepositoryReaders:          repositoryReaders(os.Getenv("GATEWAY_REPOSITORY_READERS")),
		RepositoryCacheQuotas:      repositoryCacheQuotas(os.Getenv("GATEWAY_REPOSITORY_CACHE_QUOTAS")),
		OIDCIssuer:                 strings.TrimRight(strings.TrimSpace(os.Getenv("GATEWAY_OIDC_ISSUER")), "/"),
		OIDCAudience:               strings.TrimSpace(os.Getenv("GATEWAY_OIDC_AUDIENCE")),
		OIDCJWKSURL:                strings.TrimSpace(os.Getenv("GATEWAY_OIDC_JWKS_URL")),
		OIDCClientID:               strings.TrimSpace(os.Getenv("GATEWAY_OIDC_CLIENT_ID")),
		OIDCClientSecret:           strings.TrimSpace(os.Getenv("GATEWAY_OIDC_CLIENT_SECRET")),
		OIDCRedirectURL:            strings.TrimSpace(os.Getenv("GATEWAY_OIDC_REDIRECT_URL")),
		OIDCScopes:                 oidcScopes(os.Getenv("GATEWAY_OIDC_SCOPES")),
		OIDCAdminSubjects:          splitCSV(os.Getenv("GATEWAY_OIDC_ADMIN_SUBJECTS")),
		OIDCRoles: authorization.OIDCRoleMapping{
			Reader: splitCSV(os.Getenv("GATEWAY_OIDC_READER_ROLES")),
			Writer: splitCSV(os.Getenv("GATEWAY_OIDC_WRITER_ROLES")),
			Admin:  splitCSV(os.Getenv("GATEWAY_OIDC_ADMIN_ROLES")),
		},
		OTLPHTTPEndpoint:  strings.TrimSpace(os.Getenv("GATEWAY_OTLP_HTTP_ENDPOINT")),
		OTELSamplingRatio: 1,
	}
	if err := validateRuntimeConfig(cfg); err != nil {
		return Config{}, err
	}

	if cfg.DatabaseURL == "" || cfg.S3Endpoint == "" || cfg.S3Bucket == "" || cfg.S3AccessKey == "" || cfg.S3SecretKey == "" || cfg.AdminToken == "" || cfg.ResolverToken == "" {
		return Config{}, fmt.Errorf("GATEWAY_DATABASE_URL, GATEWAY_S3_ENDPOINT, GATEWAY_S3_BUCKET, GATEWAY_S3_ACCESS_KEY, GATEWAY_S3_SECRET_KEY, GATEWAY_ADMIN_TOKEN, and GATEWAY_RESOLVER_TOKEN are required")
	}
	if _, err := url.ParseRequestURI(cfg.DatabaseURL); err != nil {
		return Config{}, fmt.Errorf("GATEWAY_DATABASE_URL is not a valid URL")
	}
	if _, err := url.ParseRequestURI(cfg.S3Endpoint); err != nil {
		return Config{}, fmt.Errorf("GATEWAY_S3_ENDPOINT is not a valid URL")
	}
	if err := configureScanner(&cfg); err != nil {
		return Config{}, err
	}
	if attempts, err := positiveIntEnv("GATEWAY_LOCAL_AUTH_MAX_FAILED_ATTEMPTS", cfg.LocalAuthMaxFailedAttempts, false); err != nil {
		return Config{}, err
	} else {
		cfg.LocalAuthMaxFailedAttempts = attempts
	}
	if cfg.LocalAuthMaxFailedAttempts > 100 {
		return Config{}, fmt.Errorf("GATEWAY_LOCAL_AUTH_MAX_FAILED_ATTEMPTS must not exceed 100")
	}
	if lockout, err := durationEnv("GATEWAY_LOCAL_AUTH_LOCKOUT_DURATION", cfg.LocalAuthLockoutDuration); err != nil {
		return Config{}, err
	} else {
		cfg.LocalAuthLockoutDuration = lockout
	}
	if cfg.LocalAuthLockoutDuration < time.Minute || cfg.LocalAuthLockoutDuration > 24*time.Hour {
		return Config{}, fmt.Errorf("GATEWAY_LOCAL_AUTH_LOCKOUT_DURATION must be between 1m and 24h")
	}
	if cfg.OIDCIssuer != "" {
		if cfg.OIDCAudience == "" {
			return Config{}, fmt.Errorf("GATEWAY_OIDC_AUDIENCE is required when GATEWAY_OIDC_ISSUER is set")
		}
		if parsed, err := url.ParseRequestURI(cfg.OIDCIssuer); err != nil || parsed.Host == "" || !secureOrLoopbackHTTP(parsed) {
			return Config{}, fmt.Errorf("GATEWAY_OIDC_ISSUER must use HTTPS outside localhost")
		}
		if cfg.OIDCJWKSURL != "" {
			if parsed, err := url.ParseRequestURI(cfg.OIDCJWKSURL); err != nil || parsed.Host == "" || !secureOrLoopbackHTTP(parsed) {
				return Config{}, fmt.Errorf("GATEWAY_OIDC_JWKS_URL must use HTTPS outside localhost")
			}
		}
		if cfg.OIDCClientID != "" {
			if cfg.OIDCRedirectURL == "" {
				return Config{}, fmt.Errorf("GATEWAY_OIDC_REDIRECT_URL is required when GATEWAY_OIDC_CLIENT_ID is set")
			}
			parsed, err := url.ParseRequestURI(cfg.OIDCRedirectURL)
			if err != nil || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
				return Config{}, fmt.Errorf("GATEWAY_OIDC_REDIRECT_URL must be an HTTP or HTTPS URL")
			}
			if parsed.Scheme == "http" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" {
				return Config{}, fmt.Errorf("GATEWAY_OIDC_REDIRECT_URL must use HTTPS outside localhost")
			}
		} else if cfg.OIDCClientSecret != "" || cfg.OIDCRedirectURL != "" {
			return Config{}, fmt.Errorf("GATEWAY_OIDC_CLIENT_ID is required when OIDC browser login is configured")
		}
	} else if cfg.OIDCClientID != "" || cfg.OIDCClientSecret != "" || cfg.OIDCRedirectURL != "" {
		return Config{}, fmt.Errorf("GATEWAY_OIDC_ISSUER is required for OIDC browser login")
	}
	if raw := strings.TrimSpace(os.Getenv("GATEWAY_OTEL_SAMPLING_RATIO")); raw != "" {
		ratio, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
			return Config{}, fmt.Errorf("GATEWAY_OTEL_SAMPLING_RATIO must be between 0 and 1")
		}
		cfg.OTELSamplingRatio = ratio
	}
	if value, err := positiveIntEnv("GATEWAY_DATABASE_MAX_OPEN_CONNS", cfg.DatabasePool.MaxOpenConns, false); err != nil {
		return Config{}, err
	} else {
		cfg.DatabasePool.MaxOpenConns = value
	}
	if value, err := positiveIntEnv("GATEWAY_DATABASE_MAX_IDLE_CONNS", cfg.DatabasePool.MaxIdleConns, true); err != nil {
		return Config{}, err
	} else {
		cfg.DatabasePool.MaxIdleConns = value
	}
	if ttl, err := durationEnv("GATEWAY_DATABASE_CONN_MAX_LIFETIME", cfg.DatabasePool.ConnMaxLifetime); err != nil {
		return Config{}, err
	} else {
		cfg.DatabasePool.ConnMaxLifetime = ttl
	}
	if ttl, err := durationEnv("GATEWAY_DATABASE_CONN_MAX_IDLE_TIME", cfg.DatabasePool.ConnMaxIdleTime); err != nil {
		return Config{}, err
	} else {
		cfg.DatabasePool.ConnMaxIdleTime = ttl
	}
	if value, err := positiveIntEnv("GATEWAY_DATABASE_COORDINATOR_MAX_OPEN_CONNS", cfg.DatabaseCoordinatorPool.MaxOpenConns, false); err != nil {
		return Config{}, err
	} else {
		cfg.DatabaseCoordinatorPool.MaxOpenConns = value
	}
	if value, err := positiveIntEnv("GATEWAY_DATABASE_COORDINATOR_MAX_IDLE_CONNS", cfg.DatabaseCoordinatorPool.MaxIdleConns, true); err != nil {
		return Config{}, err
	} else {
		cfg.DatabaseCoordinatorPool.MaxIdleConns = value
	}
	if value, err := positiveIntEnv("GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_OPEN_CONNS", cfg.DatabaseArtifactLockPool.MaxOpenConns, false); err != nil {
		return Config{}, err
	} else {
		cfg.DatabaseArtifactLockPool.MaxOpenConns = value
	}
	if value, err := positiveIntEnv("GATEWAY_DATABASE_ARTIFACT_LOCK_MAX_IDLE_CONNS", cfg.DatabaseArtifactLockPool.MaxIdleConns, true); err != nil {
		return Config{}, err
	} else {
		cfg.DatabaseArtifactLockPool.MaxIdleConns = value
	}
	cfg.DatabaseCoordinatorPool.ConnMaxLifetime = cfg.DatabasePool.ConnMaxLifetime
	cfg.DatabaseCoordinatorPool.ConnMaxIdleTime = cfg.DatabasePool.ConnMaxIdleTime
	cfg.DatabaseArtifactLockPool.ConnMaxLifetime = cfg.DatabasePool.ConnMaxLifetime
	cfg.DatabaseArtifactLockPool.ConnMaxIdleTime = cfg.DatabasePool.ConnMaxIdleTime
	if err := cfg.DatabasePool.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid database pool configuration: %w", err)
	}
	if err := cfg.DatabaseCoordinatorPool.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid database coordinator pool configuration: %w", err)
	}
	if err := cfg.DatabaseArtifactLockPool.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid database artifact lock pool configuration: %w", err)
	}
	if raw := strings.TrimSpace(os.Getenv("GATEWAY_RAW_CACHE_MAX_OBJECT_BYTES")); raw != "" {
		bytes, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || bytes <= 0 {
			return Config{}, fmt.Errorf("GATEWAY_RAW_CACHE_MAX_OBJECT_BYTES must be a positive integer")
		}
		cfg.RawCacheMaxObjectBytes = bytes
	}
	if raw := strings.TrimSpace(os.Getenv("GATEWAY_CONAN_CACHE_MAX_OBJECT_BYTES")); raw != "" {
		bytes, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || bytes <= 0 {
			return Config{}, fmt.Errorf("GATEWAY_CONAN_CACHE_MAX_OBJECT_BYTES must be a positive integer")
		}
		cfg.ConanCacheMaxObjectBytes = bytes
	}
	if ttl, err := durationEnv("GATEWAY_OCI_CACHE_TTL", cfg.OCICacheTTL); err != nil {
		return Config{}, err
	} else {
		cfg.OCICacheTTL = ttl
	}
	if ttl, err := durationEnv("GATEWAY_MAVEN_CACHE_TTL", cfg.MavenCacheTTL); err != nil {
		return Config{}, err
	} else {
		cfg.MavenCacheTTL = ttl
	}
	if ttl, err := durationEnv("GATEWAY_MAVEN_METADATA_CACHE_TTL", cfg.MavenMetadataCacheTTL); err != nil {
		return Config{}, err
	} else {
		cfg.MavenMetadataCacheTTL = ttl
	}
	if ttl, err := durationEnv("GATEWAY_MAVEN_NEGATIVE_CACHE_TTL", cfg.MavenNegativeCacheTTL); err != nil {
		return Config{}, err
	} else {
		cfg.MavenNegativeCacheTTL = ttl
	}
	if ttl, err := durationEnv("GATEWAY_NPM_METADATA_CACHE_TTL", cfg.NPMMetadataCacheTTL); err != nil {
		return Config{}, err
	} else {
		cfg.NPMMetadataCacheTTL = ttl
	}
	if ttl, err := durationEnv("GATEWAY_NPM_NEGATIVE_CACHE_TTL", cfg.NPMNegativeCacheTTL); err != nil {
		return Config{}, err
	} else {
		cfg.NPMNegativeCacheTTL = ttl
	}
	if ttl, err := durationEnv("GATEWAY_NPM_PROXY_BREAKER_TTL", cfg.NPMProxyBreakerTTL); err != nil {
		return Config{}, err
	} else {
		cfg.NPMProxyBreakerTTL = ttl
	}
	if ttl, err := durationEnv("GATEWAY_RAW_CACHE_TTL", cfg.RawCacheTTL); err != nil {
		return Config{}, err
	} else {
		cfg.RawCacheTTL = ttl
	}
	if ttl, err := durationEnv("GATEWAY_CONAN_CACHE_TTL", cfg.ConanCacheTTL); err != nil {
		return Config{}, err
	} else {
		cfg.ConanCacheTTL = ttl
	}
	if ttl, err := durationEnv("GATEWAY_RUNTIME_NODE_RETENTION", cfg.RuntimeNodeRetention); err != nil {
		return Config{}, err
	} else {
		cfg.RuntimeNodeRetention = ttl
	}
	if interval, err := durationEnv("GATEWAY_RUNTIME_NODE_PRUNE_INTERVAL", cfg.RuntimeNodePruneInterval); err != nil {
		return Config{}, err
	} else {
		cfg.RuntimeNodePruneInterval = interval
	}
	return cfg, nil
}

func secureOrLoopbackHTTP(parsed *url.URL) bool {
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" {
		return false
	}
	switch parsed.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func configureScanner(cfg *Config) error {
	requestedWithoutEndpoint := cfg.ScannerHealthEndpoint != "" || cfg.ScannerName != "" || cfg.ScannerToken != "" || strings.TrimSpace(os.Getenv("GATEWAY_SCANNER_FORMATS")) != "" || strings.TrimSpace(os.Getenv("GATEWAY_SCANNER_TIMEOUT")) != "" || strings.TrimSpace(os.Getenv("GATEWAY_SCANNER_HEALTH_TIMEOUT")) != "" || strings.TrimSpace(os.Getenv("GATEWAY_SCANNER_DATABASE_MAX_AGE")) != "" || strings.TrimSpace(os.Getenv("GATEWAY_SCANNER_MAX_RESPONSE_BYTES")) != "" || strings.TrimSpace(os.Getenv("GATEWAY_SCANNER_MAX_ARTIFACT_BYTES")) != ""
	if cfg.ScannerEndpoint == "" {
		if requestedWithoutEndpoint {
			return fmt.Errorf("GATEWAY_SCANNER_ENDPOINT is required when artifact scanner settings are configured")
		}
		cfg.ScannerName = ""
		cfg.ScannerToken = ""
		cfg.ScannerFormats = []string{}
		return nil
	}
	parsed, err := url.ParseRequestURI(cfg.ScannerEndpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !secureOrLoopbackHTTP(parsed) {
		return fmt.Errorf("GATEWAY_SCANNER_ENDPOINT must be an HTTPS URL without credentials, query, or fragment outside localhost")
	}
	if cfg.ScannerHealthEndpoint != "" {
		parsed, err = url.ParseRequestURI(cfg.ScannerHealthEndpoint)
		if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || !secureOrLoopbackHTTP(parsed) {
			return fmt.Errorf("GATEWAY_SCANNER_HEALTH_ENDPOINT must be an HTTPS URL without credentials, query, or fragment outside localhost")
		}
	}
	if cfg.ScannerName == "" {
		cfg.ScannerName = "artifact-scanner"
	}
	if len(cfg.ScannerName) > 128 || strings.ContainsAny(cfg.ScannerName, "\r\n\x00") || strings.ContainsAny(cfg.ScannerToken, "\r\n") {
		return fmt.Errorf("artifact scanner name or token is invalid")
	}
	if cfg.ScannerTimeout, err = durationEnv("GATEWAY_SCANNER_TIMEOUT", cfg.ScannerTimeout); err != nil {
		return err
	}
	if cfg.ScannerTimeout < time.Second || cfg.ScannerTimeout > 30*time.Minute {
		return fmt.Errorf("GATEWAY_SCANNER_TIMEOUT must be between 1s and 30m")
	}
	if cfg.ScannerHealthTimeout, err = durationEnv("GATEWAY_SCANNER_HEALTH_TIMEOUT", cfg.ScannerHealthTimeout); err != nil {
		return err
	}
	if cfg.ScannerHealthTimeout < time.Second || cfg.ScannerHealthTimeout > 30*time.Second {
		return fmt.Errorf("GATEWAY_SCANNER_HEALTH_TIMEOUT must be between 1s and 30s")
	}
	if cfg.ScannerDatabaseMaxAge, err = durationEnv("GATEWAY_SCANNER_DATABASE_MAX_AGE", cfg.ScannerDatabaseMaxAge); err != nil {
		return err
	}
	if cfg.ScannerDatabaseMaxAge < time.Minute || cfg.ScannerDatabaseMaxAge > 30*24*time.Hour {
		return fmt.Errorf("GATEWAY_SCANNER_DATABASE_MAX_AGE must be between 1m and 720h")
	}
	if cfg.ScannerMaxResponseBytes, err = positiveInt64Env("GATEWAY_SCANNER_MAX_RESPONSE_BYTES", cfg.ScannerMaxResponseBytes); err != nil {
		return err
	}
	if cfg.ScannerMaxResponseBytes < 1024 || cfg.ScannerMaxResponseBytes > 8<<20 {
		return fmt.Errorf("GATEWAY_SCANNER_MAX_RESPONSE_BYTES must be between 1024 and 8388608")
	}
	if cfg.ScannerMaxArtifactBytes, err = positiveInt64Env("GATEWAY_SCANNER_MAX_ARTIFACT_BYTES", cfg.ScannerMaxArtifactBytes); err != nil {
		return err
	}
	if cfg.ScannerMaxArtifactBytes > 1<<40 {
		return fmt.Errorf("GATEWAY_SCANNER_MAX_ARTIFACT_BYTES must not exceed 1099511627776")
	}
	cfg.ScannerFormats = parseFilter(os.Getenv("GATEWAY_SCANNER_FORMATS"), supportedScannerFormats)
	for _, format := range cfg.ScannerFormats {
		if _, ok := supportedScannerFormats[format]; !ok {
			return fmt.Errorf("GATEWAY_SCANNER_FORMATS contains unsupported format %q", format)
		}
	}
	return nil
}

func oidcScopes(raw string) []string {
	values := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	seen := map[string]bool{"openid": true}
	scopes := []string{"openid"}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		scopes = append(scopes, value)
	}
	if len(scopes) == 1 {
		scopes = append(scopes, "profile", "email")
	}
	return scopes
}

func (c Config) HasRole(role NodeRole) bool {
	for _, configured := range c.NodeRoles {
		if configured == NodeRoleStandalone || configured == role {
			return true
		}
	}
	return false
}

func (c Config) WorkerEnabled(format, kind string) bool {
	if !c.HasRole(NodeRoleWorker) {
		return false
	}
	return c.WorkerFormatEnabled(format) && c.WorkerKindEnabled(kind)
}

func (c Config) WorkerFormatEnabled(format string) bool {
	return contains(c.WorkerFormats, format)
}

func (c Config) WorkerKindEnabled(kind string) bool {
	return contains(c.WorkerKinds, kind)
}

func (c Config) ScannerEnabled() bool {
	return c.ScannerEndpoint != ""
}

func (c Config) ScannerFormatEnabled(format string) bool {
	return c.ScannerEnabled() && contains(c.ScannerFormats, format)
}

func validateRuntimeConfig(cfg Config) error {
	if strings.TrimSpace(cfg.InstanceID) == "" {
		return fmt.Errorf("GATEWAY_INSTANCE_ID must not be empty")
	}
	if len(cfg.NodeRoles) == 0 {
		return fmt.Errorf("GATEWAY_NODE_ROLES must include at least one role")
	}
	standalone := false
	for _, role := range cfg.NodeRoles {
		if _, ok := supportedNodeRoles[role]; !ok {
			return fmt.Errorf("GATEWAY_NODE_ROLES contains unsupported role %q", role)
		}
		if role == NodeRoleStandalone {
			standalone = true
		}
	}
	if standalone && len(cfg.NodeRoles) != 1 {
		return fmt.Errorf("GATEWAY_NODE_ROLES=standalone cannot be combined with other roles")
	}
	for _, format := range cfg.WorkerFormats {
		if _, ok := supportedWorkerFormats[format]; !ok {
			return fmt.Errorf("GATEWAY_WORKER_FORMATS contains unsupported format %q", format)
		}
	}
	for _, kind := range cfg.WorkerKinds {
		if _, ok := supportedWorkerKinds[kind]; !ok {
			return fmt.Errorf("GATEWAY_WORKER_KINDS contains unsupported kind %q", kind)
		}
	}
	return nil
}

func parseNodeRoles(raw string) []NodeRole {
	values := splitCSV(raw)
	if len(values) == 0 {
		return []NodeRole{NodeRoleStandalone}
	}
	roles := make([]NodeRole, 0, len(values))
	seen := make(map[NodeRole]struct{}, len(values))
	for _, value := range values {
		role := NodeRole(strings.ToLower(value))
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	return roles
}

func parseFilter(raw string, supported map[string]struct{}) []string {
	values := splitCSV(raw)
	if len(values) == 0 {
		for value := range supported {
			values = append(values, value)
		}
		// Map iteration is intentionally normalized so the effective config is
		// deterministic in logs and tests.
		sort.Strings(values)
		return values
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(value)
		if _, ok := supported[value]; !ok {
			result = append(result, value)
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "local"
	}
	return strings.TrimSpace(name)
}

func positiveIntEnv(name string, fallback int, allowZero bool) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || !allowZero && value == 0 {
		if allowZero {
			return 0, fmt.Errorf("%s must be a non-negative integer", name)
		}
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func positiveInt64Env(name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

// durationEnv parses a cache TTL override. The value is either a Go duration
// string ("30m", "1h30m") or a positive integer number of seconds ("900").
// An unset variable retains the fallback.
func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if seconds <= 0 {
			return 0, fmt.Errorf("%s must be a positive duration", name)
		}
		return time.Duration(seconds) * time.Second, nil
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return ttl, nil
}

func splitCSV(raw string) []string {
	var values []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			values = append(values, value)
		}
	}
	return values
}

// repositoryReaders parses semicolon-separated actor grants. Each grant is
// actor=repository-pattern|repository-pattern, where a trailing /* matches a
// repository prefix. An omitted variable retains the local-development
// unrestricted resolver token behavior.
func repositoryReaders(raw string) map[string][]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	readers := make(map[string][]string)
	for _, grant := range strings.Split(raw, ";") {
		actor, patterns, found := strings.Cut(strings.TrimSpace(grant), "=")
		if !found || strings.TrimSpace(actor) == "" {
			continue
		}
		for _, pattern := range strings.Split(patterns, "|") {
			if pattern = strings.TrimSpace(pattern); pattern != "" {
				readers[strings.TrimSpace(actor)] = append(readers[strings.TrimSpace(actor)], pattern)
			}
		}
	}
	return readers
}

func repositoryCacheQuotas(raw string) map[string]int64 {
	quotas := make(map[string]int64)
	for _, entry := range strings.Split(raw, ";") {
		repository, limit, found := strings.Cut(strings.TrimSpace(entry), "=")
		if !found || strings.TrimSpace(repository) == "" {
			continue
		}
		bytes, err := strconv.ParseInt(strings.TrimSpace(limit), 10, 64)
		if err == nil && bytes > 0 {
			quotas[strings.TrimSpace(repository)] = bytes
		}
	}
	return quotas
}

func value(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
