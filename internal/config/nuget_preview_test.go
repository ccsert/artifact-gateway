package config

import "testing"

func TestNuGetPreviewRequiresExplicitTrueConfiguration(t *testing.T) {
	for key, value := range map[string]string{"GATEWAY_DATABASE_URL": "postgres://gateway:owned@localhost/gateway", "GATEWAY_RUSTFS_ENDPOINT": "http://localhost:9000", "GATEWAY_RUSTFS_BUCKET": "owned-nuget", "GATEWAY_RUSTFS_ACCESS_KEY": "owned", "GATEWAY_RUSTFS_SECRET_KEY": "owned-secret", "GATEWAY_ADMIN_TOKEN": "owned-admin", "GATEWAY_RESOLVER_TOKEN": "owned-reader"} {
		t.Setenv(key, value)
	}
	for _, input := range []string{"", "false", "true", "yes", "1"} {
		t.Setenv("GATEWAY_NUGET_PREVIEW_ENABLED", input)
		cfg, err := Load()
		if input == "yes" || input == "1" {
			if err == nil {
				t.Fatalf("invalid opt-in accepted %q", input)
			}
			continue
		}
		if err != nil || cfg.NuGetPreviewEnabled != (input == "true") || contains(cfg.WorkerFormats, "nuget") != (input == "true") {
			t.Fatalf("opt-in=%q enabled=%t error=%v", input, cfg.NuGetPreviewEnabled, err)
		}
	}
	t.Setenv("GATEWAY_WORKER_FORMATS", "nuget")
	for _, enabled := range []string{"false", "true"} {
		t.Setenv("GATEWAY_NUGET_PREVIEW_ENABLED", enabled)
		_, err := Load()
		if (err == nil) != (enabled == "true") {
			t.Fatalf("preview worker format enabled=%s error=%v", enabled, err)
		}
	}
}
