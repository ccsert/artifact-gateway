package main

import (
	"strings"
	"testing"
)

func TestLoadConfigUsesPrivateBoundedDefaults(t *testing.T) {
	config, err := loadConfig(func(name string) string {
		if name == "REFERENCE_APT_SIGNER_TOKEN" {
			return strings.Repeat("t", 32)
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.ListenAddress != "127.0.0.1:18083" || config.KeyFile != "/var/lib/reference-apt-signer/apt-release-private.gpg" ||
		config.Name != "Artifact Gateway Local APT" || config.Email != "apt-release@artifact-gateway.local" ||
		config.RSABits != 4096 || config.MaxReleaseBytes != 16<<20 {
		t.Fatalf("config=%#v", config)
	}
}

func TestLoadConfigRejectsPublicListenerAndUnsafeSecrets(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		values map[string]string
	}{
		{name: "public listener", values: map[string]string{"REFERENCE_APT_SIGNER_LISTEN_ADDRESS": "0.0.0.0:18083", "REFERENCE_APT_SIGNER_TOKEN": strings.Repeat("t", 32)}},
		{name: "missing token", values: map[string]string{}},
		{name: "short token", values: map[string]string{"REFERENCE_APT_SIGNER_TOKEN": "short"}},
		{name: "relative key path", values: map[string]string{"REFERENCE_APT_SIGNER_TOKEN": strings.Repeat("t", 32), "REFERENCE_APT_SIGNER_KEY_FILE": "key.gpg"}},
		{name: "weak RSA key", values: map[string]string{"REFERENCE_APT_SIGNER_TOKEN": strings.Repeat("t", 32), "REFERENCE_APT_SIGNER_RSA_BITS": "1024"}},
		{name: "oversized release", values: map[string]string{"REFERENCE_APT_SIGNER_TOKEN": strings.Repeat("t", 32), "REFERENCE_APT_SIGNER_MAX_RELEASE_BYTES": "16777217"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := loadConfig(func(name string) string { return testCase.values[name] }); err == nil {
				t.Fatal("loadConfig() error=nil")
			}
		})
	}
}
