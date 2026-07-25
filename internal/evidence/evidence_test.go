package evidence

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectorWritesRedactedEvidence(t *testing.T) {
	token := "administrator-token-must-not-appear"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(http.StatusNoContent)
		case "/metrics":
			_, _ = w.Write([]byte("artifact_gateway_oci_cache_requests_total{outcome=\"hit\"} 3\nprivate_metric{actor=\"release-admin\"} 7\n"))
		case "/api/v1/audits":
			_, _ = w.Write([]byte(`[{"Outcome":"access_denied","Format":"oci","Actor":"release-admin","Resource":"secret/path","UpstreamHost":"private.example.test"}]`))
		case "/api/v1/operations/cache":
			_, _ = w.Write([]byte(`{"object_count":2,"bytes":12,"pending_candidates":1,"last_error":"storage secret","successful_runs":3,"failed_runs":1}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "evidence")
	manifest, err := (Collector{}).Collect(context.Background(), Options{GatewayURL: server.URL, Token: token, OutputDir: output, Revision: "abc123", Image: "gateway:test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Endpoints) != 4 || manifest.TargetSHA256 == "" || manifest.TargetSHA256 == server.URL {
		t.Fatalf("manifest = %#v", manifest)
	}
	for _, name := range []string{"manifest.json", "readyz.json", "metrics.json", "audits.json", "cache_operations.json"} {
		content, err := os.ReadFile(filepath.Join(output, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{token, server.URL, "release-admin", "secret/path", "private.example.test", "storage secret", "private_metric"} {
			if strings.Contains(string(content), secret) {
				t.Fatalf("%s leaked %q: %s", name, secret, content)
			}
		}
	}
	metrics, err := os.ReadFile(filepath.Join(output, "metrics.json"))
	if err != nil || !strings.Contains(string(metrics), "artifact_gateway_oci_cache_requests_total") {
		t.Fatalf("metrics=%s err=%v", metrics, err)
	}
}

func TestCollectorRefusesNonEmptyOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (Collector{}).Collect(context.Background(), Options{GatewayURL: "https://gateway.example.test", Token: "token", OutputDir: dir})
	if err == nil || !strings.Contains(err.Error(), "must be empty") {
		t.Fatalf("error = %v", err)
	}
}
