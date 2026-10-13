package agctl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

func TestConfigProfileLifecycle(t *testing.T) {
	isolatedCLI(t)
	configPath := filepath.Join(os.Getenv("HOME"), ".config", "agctl", "config.yaml")

	code, stdout, _ := runCLI(t, "config", "profile", "list")
	if code != ExitOK || !strings.Contains(stdout, "no profiles configured") {
		t.Fatalf("empty list: code = %d, stdout = %q", code, stdout)
	}

	code, stdout, stderr := runCLI(t, "config", "profile", "add", "prod", "--server", "https://gateway.example.com")
	if code != ExitOK || stderr != "" || !strings.Contains(stdout, `profile "prod" stored`) {
		t.Fatalf("add: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	info, err := os.Stat(configPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v, err = %v", info.Mode(), err)
	}
	var stored configFile
	if err := yaml.Unmarshal([]byte(readConfigContent(t, configPath)), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Profiles["prod"].Server != "https://gateway.example.com" || stored.Profiles["prod"].Token != "" {
		t.Fatalf("stored = %#v", stored)
	}

	code, stdout, _ = runCLI(t, "config", "profile", "add", "backup", "--server", "https://backup.example.com", "--insecure")
	if code != ExitOK || !strings.Contains(stdout, "insecure: yes") {
		t.Fatalf("second add: code = %d, stdout = %q", code, stdout)
	}

	code, stdout, _ = runCLI(t, "config", "profile", "list", "--json")
	if code != ExitOK {
		t.Fatalf("list json: code = %d", code)
	}
	envelope := decodeEnvelope(t, stdout)
	var page struct {
		Items []profileRecord `json:"items"`
	}
	if err := json.Unmarshal(envelope.Data, &page); err != nil {
		t.Fatalf("data: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].Name != "backup" || page.Items[1].Name != "prod" || page.Items[0].Insecure != true {
		t.Fatalf("items = %#v", page.Items)
	}
	var meta struct {
		Pagination struct {
			Mode string `json:"mode"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(envelope.Meta, &meta); err != nil || meta.Pagination.Mode != "none" {
		t.Fatalf("meta = %s", envelope.Meta)
	}

	code, stdout, _ = runCLI(t, "config", "profile", "use", "backup")
	if code != ExitOK || !strings.Contains(stdout, `current profile is now "backup"`) {
		t.Fatalf("use: code = %d, stdout = %q", code, stdout)
	}
	if err := yaml.Unmarshal([]byte(readConfigContent(t, configPath)), &stored); err != nil || stored.CurrentProfile != "backup" {
		t.Fatalf("current_profile = %q, err = %v", stored.CurrentProfile, err)
	}

	code, stdout, _ = runCLI(t, "config", "profile", "remove", "backup")
	if code != ExitOK || !strings.Contains(stdout, "current_profile is now unset") {
		t.Fatalf("remove: code = %d, stdout = %q", code, stdout)
	}
	stored = configFile{}
	if err := yaml.Unmarshal([]byte(readConfigContent(t, configPath)), &stored); err != nil || stored.CurrentProfile != "" {
		t.Fatalf("after remove: current_profile = %q, err = %v", stored.CurrentProfile, err)
	}
	if _, ok := stored.Profiles["backup"]; ok {
		t.Fatalf("backup profile survived removal: %#v", stored.Profiles)
	}

	code, _, stderr = runCLI(t, "config", "profile", "remove", "missing")
	if code != ExitUsage || !strings.Contains(stderr, `profile "missing" does not exist`) {
		t.Fatalf("remove missing: code = %d, stderr = %q", code, stderr)
	}
}

func TestConfigProfileAddTokenNeverPrinted(t *testing.T) {
	isolatedCLI(t)
	configPath := filepath.Join(os.Getenv("HOME"), ".config", "agctl", "config.yaml")

	code, stdout, stderr := runCLIStdin(t, "token-abcdef\n", "config", "profile", "add", "prod", "--server", "https://gateway.example.com", "--token-stdin")
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "token-abcdef") || strings.Contains(stderr, "token-abcdef") {
		t.Fatalf("token leaked: stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stdout, "token: yes") {
		t.Fatalf("stdout = %q", stdout)
	}
	if content := readConfigContent(t, configPath); !strings.Contains(content, "token-abcdef") {
		t.Fatalf("token not stored: %s", content)
	}

	code, stdout, _ = runCLI(t, "config", "profile", "list", "--json")
	if code != ExitOK || strings.Contains(stdout, "token-abcdef") || !strings.Contains(stdout, `"tokenStored": true`) {
		t.Fatalf("list leaked or misreported the token: %q", stdout)
	}
}

func TestConfigProfileAddValidation(t *testing.T) {
	isolatedCLI(t)
	writeHomeConfig(t, "profiles:\n  prod:\n    server: https://existing.example.com\n    token: keep-me\n")

	code, _, stderr := runCLI(t, "config", "profile", "add", "fresh")
	if code != ExitUsage || !strings.Contains(stderr, "--server is required") {
		t.Fatalf("new profile without server: code = %d, stderr = %q", code, stderr)
	}
	code, _, stderr = runCLI(t, "config", "profile", "add", "bad name", "--server", "https://x.example.com")
	if code != ExitUsage || !strings.Contains(stderr, "profile name") {
		t.Fatalf("bad name: code = %d, stderr = %q", code, stderr)
	}

	// Updating an existing profile without --server keeps its server and token.
	code, stdout, _ := runCLI(t, "config", "profile", "add", "prod")
	if code != ExitOK || !strings.Contains(stdout, "https://existing.example.com") {
		t.Fatalf("update: code = %d, stdout = %q", code, stdout)
	}
	var stored configFile
	if err := yaml.Unmarshal([]byte(readConfigContent(t, filepath.Join(os.Getenv("HOME"), ".config", "agctl", "config.yaml"))), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Profiles["prod"].Token != "keep-me" || stored.Profiles["prod"].Server != "https://existing.example.com" {
		t.Fatalf("update dropped fields: %#v", stored.Profiles["prod"])
	}
}

func TestConfigProfileCommandsRefuseLaxFile(t *testing.T) {
	isolatedCLI(t)
	path := writeHomeConfig(t, "profiles: {}\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, "config", "profile", "add", "prod", "--server", "https://gateway.example.com")
	if code != ExitUsage || !strings.Contains(stderr, "chmod 600") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestConfigProfileAddWithoutHomeFailsCleanly(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("AGCTL_SERVER", "")
	t.Setenv("AGCTL_TOKEN", "")
	t.Setenv("AGCTL_PROFILE", "")
	code, _, stderr := runCLI(t, "config", "profile", "add", "prod", "--server", "https://gateway.example.com")
	if code != ExitUsage || !strings.Contains(stderr, "home directory") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}
