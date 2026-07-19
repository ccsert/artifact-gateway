package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct {
	ListenAddress          string
	DatabaseURL            string
	RedisAddress           string
	S3Endpoint             string
	S3Bucket               string
	S3AccessKey            string
	S3SecretKey            string
	OCIProxyAllowedHosts   []string
	MavenProxyAllowedHosts []string
	AdapterMode            string
	AdminToken             string
	ResolverToken          string
	AdminActor             string
	ResolverActor          string
	RepositoryReaders      map[string][]string
	GiteaUsername          string
	GiteaToken             string
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddress:          value("GATEWAY_LISTEN_ADDRESS", ":8080"),
		DatabaseURL:            os.Getenv("GATEWAY_DATABASE_URL"),
		RedisAddress:           os.Getenv("GATEWAY_REDIS_ADDRESS"),
		S3Endpoint:             os.Getenv("GATEWAY_S3_ENDPOINT"),
		S3Bucket:               os.Getenv("GATEWAY_S3_BUCKET"),
		S3AccessKey:            os.Getenv("GATEWAY_S3_ACCESS_KEY"),
		S3SecretKey:            os.Getenv("GATEWAY_S3_SECRET_KEY"),
		OCIProxyAllowedHosts:   splitCSV(os.Getenv("GATEWAY_OCI_PROXY_ALLOWED_HOSTS")),
		MavenProxyAllowedHosts: splitCSV(os.Getenv("GATEWAY_MAVEN_PROXY_ALLOWED_HOSTS")),
		AdapterMode:            value("GATEWAY_ADAPTER_MODE", "test"),
		AdminToken:             os.Getenv("GATEWAY_ADMIN_TOKEN"),
		ResolverToken:          os.Getenv("GATEWAY_RESOLVER_TOKEN"),
		AdminActor:             value("GATEWAY_ADMIN_ACTOR", "gateway-admin"),
		ResolverActor:          value("GATEWAY_RESOLVER_ACTOR", "gateway-resolver"),
		RepositoryReaders:      repositoryReaders(os.Getenv("GATEWAY_REPOSITORY_READERS")),
		GiteaUsername:          os.Getenv("GATEWAY_GITEA_USERNAME"),
		GiteaToken:             os.Getenv("GATEWAY_GITEA_TOKEN"),
	}

	if cfg.AdapterMode != "test" && cfg.AdapterMode != "gitea" {
		return Config{}, fmt.Errorf("GATEWAY_ADAPTER_MODE must be test or gitea")
	}
	if cfg.AdapterMode == "gitea" && (cfg.GiteaUsername == "" || cfg.GiteaToken == "") {
		return Config{}, fmt.Errorf("GATEWAY_GITEA_USERNAME and GATEWAY_GITEA_TOKEN are required when GATEWAY_ADAPTER_MODE is gitea")
	}
	if cfg.DatabaseURL == "" || cfg.RedisAddress == "" || cfg.S3Endpoint == "" || cfg.S3Bucket == "" || cfg.S3AccessKey == "" || cfg.S3SecretKey == "" || cfg.AdminToken == "" || cfg.ResolverToken == "" {
		return Config{}, fmt.Errorf("GATEWAY_DATABASE_URL, GATEWAY_REDIS_ADDRESS, GATEWAY_S3_ENDPOINT, GATEWAY_S3_BUCKET, GATEWAY_S3_ACCESS_KEY, GATEWAY_S3_SECRET_KEY, GATEWAY_ADMIN_TOKEN, and GATEWAY_RESOLVER_TOKEN are required")
	}
	if _, err := url.ParseRequestURI(cfg.DatabaseURL); err != nil {
		return Config{}, fmt.Errorf("GATEWAY_DATABASE_URL is not a valid URL")
	}
	if _, err := url.ParseRequestURI(cfg.S3Endpoint); err != nil {
		return Config{}, fmt.Errorf("GATEWAY_S3_ENDPOINT is not a valid URL")
	}
	return cfg, nil
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

func value(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
