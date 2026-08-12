package main

import (
	"testing"
	"time"
)

func TestLoadConfigUsesPrivateBoundedDefaults(t *testing.T) {
	config, err := loadConfig(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if config.ListenAddress != "127.0.0.1:18082" || config.TrivyBinary != "trivy" || config.ScanTimeout != 10*time.Minute || config.HealthTimeout != 10*time.Second || config.MaxArtifactBytes != 20<<30 || config.MaxOutputBytes != 64<<20 || config.MaxConcurrent != 1 || config.SBOMDir != "/var/lib/reference-scanner/sboms" || config.SBOMBaseURL != "http://127.0.0.1:18082" || config.MaxSBOMBytes != 2<<30 {
		t.Fatalf("config=%#v", config)
	}
}

func TestLoadConfigAcceptsExplicitSafeValues(t *testing.T) {
	values := map[string]string{
		"REFERENCE_SCANNER_LISTEN_ADDRESS":     "[::1]:19082",
		"REFERENCE_SCANNER_TOKEN":              "shared-secret",
		"REFERENCE_SCANNER_TRIVY_BINARY":       "/usr/local/bin/trivy",
		"REFERENCE_SCANNER_SCAN_TIMEOUT":       "3m",
		"REFERENCE_SCANNER_HEALTH_TIMEOUT":     "5s",
		"REFERENCE_SCANNER_MAX_ARTIFACT_BYTES": "1048576",
		"REFERENCE_SCANNER_MAX_OUTPUT_BYTES":   "2097152",
		"REFERENCE_SCANNER_MAX_CONCURRENT":     "2",
		"REFERENCE_SCANNER_SBOM_DIR":           "/tmp/sboms",
		"REFERENCE_SCANNER_BASE_URL":           "http://127.0.0.1:19082",
		"REFERENCE_SCANNER_MAX_SBOM_BYTES":     "4194304",
	}
	config, err := loadConfig(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	if config.ListenAddress != "[::1]:19082" || config.Token != "shared-secret" || config.MaxArtifactBytes != 1048576 || config.MaxOutputBytes != 2097152 || config.MaxConcurrent != 2 || config.SBOMDir != "/tmp/sboms" || config.MaxSBOMBytes != 4194304 {
		t.Fatalf("config=%#v", config)
	}
}

func TestLoadConfigRejectsPublicListenerAndMalformedBounds(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		values map[string]string
	}{
		{name: "public listener", values: map[string]string{"REFERENCE_SCANNER_LISTEN_ADDRESS": "0.0.0.0:18082"}},
		{name: "missing port", values: map[string]string{"REFERENCE_SCANNER_LISTEN_ADDRESS": "127.0.0.1"}},
		{name: "short timeout", values: map[string]string{"REFERENCE_SCANNER_SCAN_TIMEOUT": "100ms"}},
		{name: "invalid bytes", values: map[string]string{"REFERENCE_SCANNER_MAX_ARTIFACT_BYTES": "many"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := loadConfig(func(name string) string { return testCase.values[name] }); err == nil {
				t.Fatal("loadConfig() error=nil")
			}
		})
	}
}
