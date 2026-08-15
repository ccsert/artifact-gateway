package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
)

func TestRunnerReportsSelectedChecksInStableOrder(t *testing.T) {
	runner := NewRunner()
	runner.PostgresPing = func(context.Context, string) error { return nil }
	runner.ObjectStoreCheck = func(context.Context, config.Config) error { return nil }
	report := runner.Run(context.Background(), testConfig(), []string{"postgres", "configuration"})

	if len(report.Results) != 2 || report.Results[0].Name != "configuration" || report.Results[1].Name != "postgres" {
		t.Fatalf("results = %#v", report.Results)
	}
	if report.Failed() {
		t.Fatalf("report unexpectedly failed: %#v", report)
	}
}

func TestConfigurationReportsAPTSignerTrustWithoutExposingFingerprints(t *testing.T) {
	cfg := testConfig()
	cfg.APTSignerEndpoint = "https://signer.example.test/v1/sign-release"
	cfg.APTSignerTrustedFingerprints = []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}
	cfg.APTSignerTrustedPublicKeysFile = "/run/secrets/apt-release-public-keys.asc"
	cfg.APTSignerTrustedPublicKeys = []byte("validated public keys")
	cfg.APTSignerTLSCAFile = "/run/secrets/apt-signer-ca.pem"
	cfg.APTSignerTLSRootCertificates = []byte("validated TLS roots")
	result := configurationResult(cfg)
	if result.Details["apt_signer_enabled"] != true || result.Details["apt_signer_trusted_fingerprint_count"] != 2 ||
		result.Details["apt_signer_trusted_public_keys_validated"] != true || result.Details["apt_signer_tls_trust_mode"] != "custom_roots" {
		t.Fatalf("APT signer diagnostics=%#v", result.Details)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), strings.Repeat("a", 40)) || strings.Contains(string(encoded), strings.Repeat("b", 40)) {
		t.Fatalf("diagnostics exposed trusted fingerprints: %s", encoded)
	}
}

func TestRunnerDoesNotExposeDependencyError(t *testing.T) {
	secret := "postgres://gateway:super-secret@db/gateway"
	runner := NewRunner()
	runner.PostgresPing = func(context.Context, string) error { return errors.New(secret) }
	report := runner.Run(context.Background(), testConfig(), []string{"postgres"})

	if len(report.Results) != 1 || report.Results[0].Status != StatusFail || report.Results[0].Summary != "dependency check failed" {
		t.Fatalf("results = %#v", report.Results)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("report leaked dependency error: %s", encoded)
	}
}

func TestPolicyRejectsAllowlistURL(t *testing.T) {
	cfg := testConfig()
	cfg.OCIProxyAllowedHosts = []string{"https://registry.example.test/path"}
	result := policyResult(cfg)
	if result.Status != StatusFail || result.Summary != "proxy allowlist contains an invalid host" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunnerChecksOIDCJWKS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg := testConfig()
	cfg.OIDCIssuer = "https://issuer.example.test"
	cfg.OIDCJWKSURL = server.URL
	report := NewRunner().Run(context.Background(), cfg, []string{"oidc_jwks"})
	if len(report.Results) != 1 || report.Results[0].Status != StatusPass {
		t.Fatalf("results = %#v", report.Results)
	}
}

func TestCLIConfigurationFailureIsRedacted(t *testing.T) {
	secret := "do-not-log-this-database-secret"
	t.Setenv("GATEWAY_DATABASE_URL", secret)
	t.Setenv("GATEWAY_RUSTFS_ENDPOINT", "http://rustfs:9000")
	t.Setenv("GATEWAY_RUSTFS_BUCKET", "gateway-cache")
	t.Setenv("GATEWAY_RUSTFS_ACCESS_KEY", "access-key")
	t.Setenv("GATEWAY_RUSTFS_SECRET_KEY", "object-store-secret")
	t.Setenv("GATEWAY_ADMIN_TOKEN", "admin-token")
	t.Setenv("GATEWAY_RESOLVER_TOKEN", "resolver-token")

	var stdout, stderr bytes.Buffer
	exitCode := RunCLI(context.Background(), []string{"run", "--format", "json"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exit code = %d, stderr=%q", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stdout.String(), "object-store-secret") {
		t.Fatalf("output leaked secret: %s", stdout.String())
	}
	var report Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("output is not JSON: %v; output=%s", err, stdout.String())
	}
	if len(report.Results) != 1 || report.Results[0].Status != StatusFail {
		t.Fatalf("report = %#v", report)
	}
}

func testConfig() config.Config {
	return config.Config{
		DatabaseURL:     "postgres://gateway:password@db/gateway",
		RustFSEndpoint:  "http://rustfs:9000",
		RustFSBucket:    "gateway-cache",
		RustFSAccessKey: "access-key",
		RustFSSecretKey: "secret-key",
	}
}
