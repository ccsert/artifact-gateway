// Package preflight provides read-only release checks for an existing Gateway
// deployment. It deliberately has no dependency on protocol handlers.
package preflight

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/secrets"
	"github.com/jackc/pgx/v5"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

type Result struct {
	Name    string         `json:"name"`
	Status  Status         `json:"status"`
	Summary string         `json:"summary"`
	Details map[string]any `json:"details,omitempty"`
}

type Report struct {
	CheckedAt time.Time `json:"checked_at"`
	Results   []Result  `json:"results"`
}

func (r Report) Failed() bool {
	for _, result := range r.Results {
		if result.Status == StatusFail {
			return true
		}
	}
	return false
}

type Runner struct {
	PostgresPing     func(context.Context, string) error
	ObjectStoreCheck func(context.Context, config.Config) error
	HTTPClient       *http.Client
}

func NewRunner() Runner {
	return Runner{
		PostgresPing:     pingPostgres,
		ObjectStoreCheck: checkObjectStore,
		HTTPClient:       &http.Client{Timeout: 5 * time.Second},
	}
}

func CheckNames() []string {
	return []string{"configuration", "policy", "postgres", "object_store", "oidc_jwks"}
}

func (r Runner) Run(ctx context.Context, cfg config.Config, names []string) Report {
	selected := selectedChecks(names)
	report := Report{CheckedAt: time.Now().UTC()}
	for _, name := range CheckNames() {
		if !selected[name] {
			continue
		}
		switch name {
		case "configuration":
			report.Results = append(report.Results, configurationResult(cfg))
		case "policy":
			report.Results = append(report.Results, policyResult(cfg))
		case "postgres":
			report.Results = append(report.Results, dependencyResult(name, "PostgreSQL is reachable", r.PostgresPing(ctx, cfg.DatabaseURL)))
		case "object_store":
			report.Results = append(report.Results, dependencyResult(name, "object storage bucket is reachable", r.ObjectStoreCheck(ctx, cfg)))
		case "oidc_jwks":
			report.Results = append(report.Results, r.checkOIDC(ctx, cfg))
		}
	}
	return report
}

func selectedChecks(names []string) map[string]bool {
	selected := make(map[string]bool, len(CheckNames()))
	if len(names) == 0 {
		for _, name := range CheckNames() {
			selected[name] = true
		}
		return selected
	}
	for _, name := range names {
		selected[name] = true
	}
	return selected
}

func configurationResult(cfg config.Config) Result {
	return Result{
		Name:    "configuration",
		Status:  StatusPass,
		Summary: "required configuration loaded",
		Details: map[string]any{
			"oidc_enabled":                             cfg.OIDCIssuer != "",
			"oidc_browser_login_enabled":               cfg.OIDCClientID != "",
			"oci_proxy_allowed_host_count":             len(cfg.OCIProxyAllowedHosts),
			"maven_proxy_allowed_host_count":           len(cfg.MavenProxyAllowedHosts),
			"raw_proxy_allowed_host_count":             len(cfg.RawProxyAllowedHosts),
			"repository_grant_actor_count":             len(cfg.RepositoryReaders),
			"repository_cache_quota_count":             len(cfg.RepositoryCacheQuotas),
			"oidc_admin_subject_count":                 len(cfg.OIDCAdminSubjects),
			"settings_encryption_key_configured":       strings.TrimSpace(os.Getenv(secrets.KeyEnv)) != "",
			"artifact_scanner_enabled":                 cfg.ScannerEnabled(),
			"artifact_scanner_health_enabled":          cfg.ScannerHealthEndpoint != "",
			"artifact_scanner_format_count":            len(cfg.ScannerFormats),
			"artifact_scanner_database_max_age_s":      int64(cfg.ScannerDatabaseMaxAge / time.Second),
			"apt_signer_enabled":                       cfg.APTSignerEnabled(),
			"apt_signer_trusted_fingerprint_count":     len(cfg.APTSignerTrustedFingerprints),
			"apt_signer_trusted_public_keys_validated": len(cfg.APTSignerTrustedPublicKeys) > 0,
		},
	}
}

func policyResult(cfg config.Config) Result {
	for _, host := range append(append([]string{}, cfg.OCIProxyAllowedHosts...), append(cfg.MavenProxyAllowedHosts, cfg.RawProxyAllowedHosts...)...) {
		if !validHost(host) {
			return Result{Name: "policy", Status: StatusFail, Summary: "proxy allowlist contains an invalid host"}
		}
	}
	return Result{Name: "policy", Status: StatusPass, Summary: "proxy allowlists and configured quotas are valid"}
}

func validHost(host string) bool {
	if strings.ContainsAny(host, "/?#@") {
		return false
	}
	parsed, err := url.Parse("https://" + host)
	return err == nil && parsed.Host != "" && parsed.Hostname() != ""
}

func dependencyResult(name, success string, err error) Result {
	if err != nil {
		return Result{Name: name, Status: StatusFail, Summary: "dependency check failed"}
	}
	return Result{Name: name, Status: StatusPass, Summary: success}
}

func pingPostgres(ctx context.Context, databaseURL string) error {
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = connection.Close(ctx) }()
	return connection.Ping(ctx)
}

func checkObjectStore(ctx context.Context, cfg config.Config) error {
	store, err := objectstore.NewRustFSStore(cfg.RustFSEndpoint, cfg.RustFSAccessKey, cfg.RustFSSecretKey, cfg.RustFSBucket)
	if err != nil {
		return err
	}
	return store.CheckBucket(ctx)
}

func (r Runner) checkOIDC(ctx context.Context, cfg config.Config) Result {
	if cfg.OIDCIssuer == "" {
		return Result{Name: "oidc_jwks", Status: StatusSkip, Summary: "OIDC is not configured"}
	}
	client := r.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := cfg.OIDCJWKSURL
	if endpoint == "" {
		endpoint = cfg.OIDCIssuer + "/.well-known/openid-configuration"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err == nil {
		response, requestErr := client.Do(request)
		if requestErr != nil {
			err = requestErr
		} else {
			_ = response.Body.Close()
			if response.StatusCode >= http.StatusBadRequest {
				err = fmt.Errorf("JWKS returned HTTP %d", response.StatusCode)
			}
		}
	}
	return dependencyResult("oidc_jwks", "OIDC JWKS endpoint is reachable", err)
}
