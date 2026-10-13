package agctl

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	yaml "go.yaml.in/yaml/v3"
)

// The tests in this file guard the defects raised in the review of PR #346:
// dry-run must never execute a write, nested command failures must keep the
// JSON envelope and hide credentials, login must not target a mismatched
// profile, missing NAME must fail cleanly, and logout must not overclaim
// revocation.

func TestDryRunRefusesWriteCommands(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	fake.valid["stored-token"] = true
	fake.sessions["stored-token"] = true
	configPath := writeHomeConfig(t, fmt.Sprintf("current_profile: prod\nprofiles:\n  prod:\n    server: %s\n    token: stored-token\n", fake.server.URL))

	writes := []struct {
		name  string
		stdin string
		args  []string
	}{
		{"profile add", "", []string{"config", "profile", "add", "fresh", "--server", "https://new.example"}},
		{"profile use", "", []string{"config", "profile", "use", "prod"}},
		{"profile remove", "", []string{"config", "profile", "remove", "prod"}},
		{"auth login", fake.password + "\n", []string{"auth", "login", "--username", "alice", "--profile", "prod", "--password-stdin"}},
		{"auth logout", "", []string{"auth", "logout"}},
		{"auth change-password", fake.password + "\nreplacement-password\n", []string{"auth", "change-password", "--password-stdin"}},
	}
	for _, write := range writes {
		t.Run(write.name, func(t *testing.T) {
			args := append(append([]string{}, write.args...), "--dry-run")
			code, _, stderr := runCLIStdin(t, write.stdin, args...)
			if code != ExitUsage || !strings.Contains(stderr, "--dry-run is not supported") {
				t.Fatalf("code = %d, stderr = %q", code, stderr)
			}
		})
	}
	if fake.loginCalls != 0 || fake.logoutCall != 0 || fake.changeBody != nil {
		t.Fatalf("dry-run reached the server: login=%d logout=%d change=%#v", fake.loginCalls, fake.logoutCall, fake.changeBody)
	}
	content := readConfigContent(t, configPath)
	if !strings.Contains(content, "stored-token") || strings.Contains(content, "fresh") || !strings.Contains(content, "current_profile: prod") {
		t.Fatalf("dry-run changed the configuration: %s", content)
	}

	// The refusal also uses the JSON envelope.
	code, stdout, _ := runCLI(t, "config", "profile", "remove", "prod", "--dry-run", "--json")
	if code != ExitUsage {
		t.Fatalf("json refusal code = %d", code)
	}
	envelope := decodeEnvelope(t, stdout)
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "usage_error" || !strings.Contains(envelope.Error.Message, "--dry-run") {
		t.Fatalf("envelope = %#v", envelope)
	}

	// Read-only commands still accept --dry-run without side effects.
	if code, _, _ := runCLI(t, "config", "profile", "list", "--dry-run"); code != ExitOK {
		t.Fatalf("read-only dry-run code = %d", code)
	}
}

func TestProfileCommandsRequireName(t *testing.T) {
	isolatedCLI(t)
	writeHomeConfig(t, "profiles:\n  prod:\n    server: https://gateway.example.com\n")
	for _, args := range [][]string{
		{"config", "profile", "add", "--json"},
		{"config", "profile", "use", "--json"},
		{"config", "profile", "remove", "--json"},
	} {
		code, stdout, stderr := runCLI(t, args...)
		if code != ExitUsage || stderr != "" {
			t.Fatalf("%v: code = %d, stderr = %q", args, code, stderr)
		}
		envelope := decodeEnvelope(t, stdout)
		if envelope.OK || envelope.Error == nil || envelope.Error.Code != "usage_error" ||
			!strings.Contains(envelope.Error.Message, "missing required argument NAME") {
			t.Fatalf("%v: envelope = %#v", args, envelope)
		}
	}
}

func TestNestedCommandFailuresNeverLeakCredentials(t *testing.T) {
	isolatedCLI(t)
	const secret = "review-secret-token"
	tests := []struct {
		name     string
		args     []string
		wantJSON bool
	}{
		{"unknown subcommand", []string{"auth", "unknown", "--token", secret, "--json"}, true},
		{"invalid flag value", []string{"auth", "whoami", "--token", secret, "--timeout", secret, "--json"}, true},
		{"invalid flag", []string{"auth", "whoami", "--token", secret, "--json", "--missing"}, true},
		{"unknown command", []string{"missing", "--token", secret, "--json"}, true},
		{"extra positional", []string{"config", "profile", "add", "one", "two", "--token", secret, "--json"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, test.args...)
			if code != ExitUsage {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
			}
			combined := stdout + stderr
			if strings.Contains(combined, secret) {
				t.Fatalf("failure output leaked the credential: %q", combined)
			}
			if test.wantJSON {
				if stderr != "" {
					t.Fatalf("stderr = %q", stderr)
				}
				envelope := decodeEnvelope(t, stdout)
				if envelope.OK || envelope.Error == nil || envelope.Error.Code != "usage_error" {
					t.Fatalf("envelope = %#v", envelope)
				}
			}
		})
	}
}

func TestAuthLogoutMachineCredentialStaysValid(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	fake.valid["static-admin"] = true // valid, but not a local user session
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: static-admin\n", fake.server.URL))

	code, stdout, _ := runCLI(t, "auth", "logout", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var data authLogoutResult
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &data); err != nil {
		t.Fatalf("data: %v", err)
	}
	if !data.ServerLogoutRequested || data.CredentialInvalidated == nil || *data.CredentialInvalidated {
		t.Fatalf("data = %#v", data)
	}
	if !fake.tokenIsValid("static-admin") {
		t.Fatalf("machine credential was revoked by logout")
	}
	if content := readConfigContent(t, configPath); strings.Contains(content, "static-admin") {
		t.Fatalf("stored token survived logout: %s", content)
	}
	// The machine credential still authenticates after the logout request.
	if code, _, _ := runCLI(t, "auth", "whoami", "--token", "static-admin"); code != ExitOK {
		t.Fatalf("machine credential stopped authenticating: code = %d", code)
	}
}

func TestAuthLogoutReportsUnknownCredentialState(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: gone-token\n", fake.server.URL))

	code, stdout, _ := runCLI(t, "auth", "logout", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var data authLogoutResult
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &data); err != nil {
		t.Fatalf("data: %v", err)
	}
	if !data.ServerLogoutRequested || data.CredentialInvalidated == nil || !*data.CredentialInvalidated {
		t.Fatalf("data = %#v", data)
	}
	if !strings.Contains(data.Note, "never by this command") {
		t.Fatalf("note = %q", data.Note)
	}
}

func TestAuthLoginRefusesMismatchedProfileTarget(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	configPath := writeHomeConfig(t, "profiles:\n  prod:\n    server: https://other.example\n    token: keep-me\n")

	flagOverride := func(t *testing.T) {
		code, _, stderr := runCLIStdin(t, fake.password+"\n", "auth", "login", "--username", "alice", "--profile", "prod", "--server", fake.server.URL, "--password-stdin")
		if code != ExitUsage || !strings.Contains(stderr, "targets https://other.example") {
			t.Fatalf("code = %d, stderr = %q", code, stderr)
		}
	}
	envOverride := func(t *testing.T) {
		t.Setenv("AGCTL_SERVER", fake.server.URL)
		code, _, stderr := runCLIStdin(t, fake.password+"\n", "auth", "login", "--username", "alice", "--profile", "prod", "--password-stdin")
		if code != ExitUsage || !strings.Contains(stderr, "targets https://other.example") {
			t.Fatalf("code = %d, stderr = %q", code, stderr)
		}
	}
	t.Run("server flag", flagOverride)
	t.Run("environment", envOverride)

	if fake.loginCalls != 0 {
		t.Fatalf("mismatched login reached the server %d times", fake.loginCalls)
	}
	var stored configFile
	if err := yaml.Unmarshal([]byte(readConfigContent(t, configPath)), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Profiles["prod"].Token != "keep-me" || stored.Profiles["prod"].Server != "https://other.example" {
		t.Fatalf("mismatched login changed the profile: %#v", stored.Profiles["prod"])
	}
}

func TestAuthLoginBindsUnboundProfileServer(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	configPath := writeHomeConfig(t, "profiles:\n  prod: {}\n")

	code, stdout, stderr := runCLIStdin(t, fake.password+"\n", "auth", "login", "--username", "alice", "--profile", "prod", "--server", fake.server.URL, "--password-stdin", "--json")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var data authLoginResult
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &data); err != nil {
		t.Fatalf("data: %v", err)
	}
	if data.Server != fake.server.URL {
		t.Fatalf("data = %#v", data)
	}
	var stored configFile
	if err := yaml.Unmarshal([]byte(readConfigContent(t, configPath)), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Profiles["prod"].Server != fake.server.URL || stored.Profiles["prod"].Token == "" {
		t.Fatalf("profile was not bound to the login target: %#v", stored.Profiles["prod"])
	}
}
