package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/authorization"
)

type Config struct {
	ListenAddress            string
	DatabaseURL              string
	S3Endpoint               string
	S3Bucket                 string
	S3AccessKey              string
	S3SecretKey              string
	OCIProxyAllowedHosts     []string
	MavenProxyAllowedHosts   []string
	RawProxyAllowedHosts     []string
	RawCacheMaxObjectBytes   int64
	ConanCacheMaxObjectBytes int64
	OCICacheTTL              time.Duration
	MavenCacheTTL            time.Duration
	MavenMetadataCacheTTL    time.Duration
	MavenNegativeCacheTTL    time.Duration
	RawCacheTTL              time.Duration
	ConanCacheTTL            time.Duration
	AdminToken               string
	ResolverToken            string
	AdminActor               string
	ResolverActor            string
	RepositoryReaders        map[string][]string
	RepositoryCacheQuotas    map[string]int64
	OIDCIssuer               string
	OIDCAudience             string
	OIDCJWKSURL              string
	OIDCAdminSubjects        []string
	OIDCRoles                authorization.OIDCRoleMapping
	OTLPHTTPEndpoint         string
	OTELSamplingRatio        float64
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:            value("GATEWAY_LISTEN_ADDRESS", ":8080"),
		DatabaseURL:              os.Getenv("GATEWAY_DATABASE_URL"),
		S3Endpoint:               os.Getenv("GATEWAY_S3_ENDPOINT"),
		S3Bucket:                 os.Getenv("GATEWAY_S3_BUCKET"),
		S3AccessKey:              os.Getenv("GATEWAY_S3_ACCESS_KEY"),
		S3SecretKey:              os.Getenv("GATEWAY_S3_SECRET_KEY"),
		OCIProxyAllowedHosts:     splitCSV(os.Getenv("GATEWAY_OCI_PROXY_ALLOWED_HOSTS")),
		MavenProxyAllowedHosts:   splitCSV(os.Getenv("GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS")),
		RawProxyAllowedHosts:     splitCSV(os.Getenv("GATEWAY_RAW_PROXY_ALLOWED_HOSTS")),
		RawCacheMaxObjectBytes:   1 << 30,
		ConanCacheMaxObjectBytes: 1 << 30,
		OCICacheTTL:              15 * time.Minute,
		MavenCacheTTL:            24 * time.Hour,
		MavenMetadataCacheTTL:    15 * time.Minute,
		MavenNegativeCacheTTL:    10 * time.Minute,
		RawCacheTTL:              15 * time.Minute,
		ConanCacheTTL:            15 * time.Minute,
		AdminToken:               os.Getenv("GATEWAY_ADMIN_TOKEN"),
		ResolverToken:            os.Getenv("GATEWAY_RESOLVER_TOKEN"),
		AdminActor:               value("GATEWAY_ADMIN_ACTOR", "gateway-admin"),
		ResolverActor:            value("GATEWAY_RESOLVER_ACTOR", "gateway-resolver"),
		RepositoryReaders:        repositoryReaders(os.Getenv("GATEWAY_REPOSITORY_READERS")),
		RepositoryCacheQuotas:    repositoryCacheQuotas(os.Getenv("GATEWAY_REPOSITORY_CACHE_QUOTAS")),
		OIDCIssuer:               strings.TrimRight(strings.TrimSpace(os.Getenv("GATEWAY_OIDC_ISSUER")), "/"),
		OIDCAudience:             strings.TrimSpace(os.Getenv("GATEWAY_OIDC_AUDIENCE")),
		OIDCJWKSURL:              strings.TrimSpace(os.Getenv("GATEWAY_OIDC_JWKS_URL")),
		OIDCAdminSubjects:        splitCSV(os.Getenv("GATEWAY_OIDC_ADMIN_SUBJECTS")),
		OIDCRoles: authorization.OIDCRoleMapping{
			Reader: splitCSV(os.Getenv("GATEWAY_OIDC_READER_ROLES")),
			Writer: splitCSV(os.Getenv("GATEWAY_OIDC_WRITER_ROLES")),
			Admin:  splitCSV(os.Getenv("GATEWAY_OIDC_ADMIN_ROLES")),
		},
		OTLPHTTPEndpoint:  strings.TrimSpace(os.Getenv("GATEWAY_OTLP_HTTP_ENDPOINT")),
		OTELSamplingRatio: 1,
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
	if cfg.OIDCIssuer != "" {
		if cfg.OIDCAudience == "" {
			return Config{}, fmt.Errorf("GATEWAY_OIDC_AUDIENCE is required when GATEWAY_OIDC_ISSUER is set")
		}
		if parsed, err := url.ParseRequestURI(cfg.OIDCIssuer); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return Config{}, fmt.Errorf("GATEWAY_OIDC_ISSUER must be an HTTPS URL")
		}
		if cfg.OIDCJWKSURL != "" {
			if parsed, err := url.ParseRequestURI(cfg.OIDCJWKSURL); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				return Config{}, fmt.Errorf("GATEWAY_OIDC_JWKS_URL must be an HTTPS URL")
			}
		}
	}
	if raw := strings.TrimSpace(os.Getenv("GATEWAY_OTEL_SAMPLING_RATIO")); raw != "" {
		ratio, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
			return Config{}, fmt.Errorf("GATEWAY_OTEL_SAMPLING_RATIO must be between 0 and 1")
		}
		cfg.OTELSamplingRatio = ratio
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
	return cfg, nil
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
