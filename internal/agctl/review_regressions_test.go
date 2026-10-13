package agctl

import (
	"encoding/json"
	"fmt"
	"os"
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

// Second review round: a legitimate positional argument must not stop either
// the failure-format scan or credential collection.
func TestProfileFailureScanHandlesLegitPositionals(t *testing.T) {
	isolatedCLI(t)
	writeHomeConfig(t, "profiles:\n  prod:\n    server: https://gateway.example.com\n")
	const secret = "review-secret-token"
	tests := []struct {
		name     string
		args     []string
		wantJSON bool
	}{
		{"add unknown flag", []string{"config", "profile", "add", "prod", "--unknown", "--json"}, true},
		{"use unknown flag", []string{"config", "profile", "use", "prod", "--unknown", "--json"}, true},
		{"remove unknown flag", []string{"config", "profile", "remove", "prod", "--unknown", "--json"}, true},
		{"unknown flag before name", []string{"config", "profile", "add", "--unknown", "--json", "prod"}, true},
		{"timeout failure before token", []string{"config", "profile", "add", "prod", "--timeout", secret, "--token", secret, "--json"}, true},
		{"token after unknown flag", []string{"config", "profile", "add", "prod", "--unknown", "--token", secret, "--json"}, true},
		{"token equals form", []string{"config", "profile", "add", "prod", "--timeout", secret, "--token=" + secret, "--json"}, true},
		{"token before name", []string{"config", "profile", "add", "--token", secret, "prod", "--timeout", secret, "--json"}, true},
		{"invalid output with token", []string{"config", "profile", "add", "prod", "--token", secret, "-o", secret}, false},
		{"token before invalid output", []string{"config", "profile", "add", "prod", "--token", secret, "-o", "xml"}, false},
		{"extra positional keeps conservative format", []string{"config", "profile", "add", "one", "two", "--token", secret, "--json"}, false},
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
			} else if stdout != "" {
				t.Fatalf("stdout = %q", stdout)
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

// Second review round: a stored credential belongs to its target. Changing a
// profile's server must drop the old token instead of sending it to the new
// server later.
func TestProfileServerChangeDropsOldTargetToken(t *testing.T) {
	isolatedCLI(t)
	serverA := newAuthFakeGateway(t)
	serverB := newAuthFakeGateway(t)
	serverA.valid["a-token"] = true
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: a-token\n", serverA.server.URL))

	code, stdout, stderr := runCLI(t, "config", "profile", "add", "prod", "--server", serverB.server.URL)
	if code != ExitOK || stderr != "" || !strings.Contains(stdout, "stored token cleared: target changed") {
		t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if content := readConfigContent(t, configPath); strings.Contains(content, "a-token") {
		t.Fatalf("old token survived a target change: %s", content)
	}

	// A later invocation must not send A's token to B: it fails before any
	// request because the profile no longer holds credentials.
	code, _, stderr = runCLI(t, "auth", "whoami", "--profile", "prod")
	if code != ExitUnauthenticated || !strings.Contains(stderr, "no credentials") {
		t.Fatalf("whoami: code = %d, stderr = %q", code, stderr)
	}
	if serverA.sawToken("a-token") || serverB.sawToken("a-token") || serverB.requestCount() != 0 {
		t.Fatalf("the old target's credential reached a server: A=%t B=%t requests=%d",
			serverA.sawToken("a-token"), serverB.sawToken("a-token"), serverB.requestCount())
	}
}

func TestProfileServerChangeAcceptsNewTargetToken(t *testing.T) {
	isolatedCLI(t)
	serverA := newAuthFakeGateway(t)
	serverB := newAuthFakeGateway(t)
	serverA.valid["a-token"] = true
	serverB.valid["b-token"] = true
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: a-token\n", serverA.server.URL))

	code, _, stderr := runCLIStdin(t, "b-token\n", "config", "profile", "add", "prod", "--server", serverB.server.URL, "--token-stdin")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	code, _, _ = runCLI(t, "auth", "whoami", "--profile", "prod")
	if code != ExitOK {
		t.Fatalf("whoami against the new target failed: code = %d", code)
	}
	if !serverB.sawToken("b-token") || serverA.sawToken("a-token") || serverA.requestCount() != 0 {
		t.Fatalf("credential binding wrong: B saw new=%t, A saw old=%t, A requests=%d",
			serverB.sawToken("b-token"), serverA.sawToken("a-token"), serverA.requestCount())
	}
	if content := readConfigContent(t, configPath); strings.Contains(content, "a-token") {
		t.Fatalf("old token survived alongside the new one: %s", content)
	}
}

func TestProfileUnchangedTargetKeepsToken(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	server := fake.server.URL
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: keep-me\n", server))

	// Equivalent spellings and plain updates keep the credential.
	for _, args := range [][]string{
		{"config", "profile", "add", "prod"},
		{"config", "profile", "add", "prod", "--server", server + "/"},
	} {
		code, stdout, _ := runCLI(t, args...)
		if code != ExitOK {
			t.Fatalf("%v: code = %d", args, code)
		}
		if strings.Contains(stdout, "token cleared") {
			t.Fatalf("%v: equivalent target cleared the token: %s", args, stdout)
		}
		var stored configFile
		if err := yaml.Unmarshal([]byte(readConfigContent(t, configPath)), &stored); err != nil {
			t.Fatal(err)
		}
		if stored.Profiles["prod"].Token != "keep-me" {
			t.Fatalf("%v: token = %q", args, stored.Profiles["prod"].Token)
		}
	}
}

func TestNormalizeServerTarget(t *testing.T) {
	tests := []struct {
		input string
		want  string
		equal string
	}{
		{"https://Gateway.Example.com:443/", "https://gateway.example.com", "https://gateway.example.com"},
		{"http://gw.example.com:80", "http://gw.example.com", "http://gw.example.com/"},
		{"https://gw.example.com:8443/base/", "https://gw.example.com:8443/base", "https://gw.example.com:8443/base"},
		{"https://gw.example.com/Base", "https://gw.example.com/Base", "https://gw.example.com/base"},
	}
	for _, test := range tests {
		got, err := normalizeServerTarget(test.input)
		if err != nil {
			t.Fatalf("%q: %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("normalize(%q) = %q, want %q", test.input, got, test.want)
		}
		equivalent, err := normalizeServerTarget(test.equal)
		if err != nil {
			t.Fatalf("%q: %v", test.equal, err)
		}
		same := got == equivalent
		// Only the first three pairs are equivalent; path case is preserved.
		wantSame := test.input != "https://gw.example.com/Base"
		if same != wantSame {
			t.Fatalf("normalize(%q) == normalize(%q) = %t, want %t", test.input, test.equal, same, wantSame)
		}
	}
	if _, err := normalizeServerTarget("gateway.example.com"); err == nil {
		t.Fatal("scheme-less server accepted")
	}
	if _, err := normalizeServerTarget(""); err == nil {
		t.Fatal("empty server accepted")
	}
}

func TestProfileAddValidatesServerURL(t *testing.T) {
	isolatedCLI(t)
	configPath := writeHomeConfig(t, "profiles:\n  prod:\n    server: https://gateway.example.com\n    token: keep-me\n")
	code, _, stderr := runCLI(t, "config", "profile", "add", "fresh", "--server", "not a url")
	if code != ExitUsage || !strings.Contains(stderr, "not a valid http(s) URL") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	content := readConfigContent(t, configPath)
	if strings.Contains(content, "fresh") {
		t.Fatalf("invalid server created a profile: %s", content)
	}
}

func TestProfileTargetChangeDuringLoginRefused(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n", fake.server.URL))
	fake.beforeLoginResponse = func() {
		// Simulate a concurrent `config profile add` while the login request
		// is in flight.
		_ = os.WriteFile(configPath, []byte("profiles:\n  prod:\n    server: https://other.example\n"), 0o600)
	}

	code, _, stderr := runCLIStdin(t, fake.password+"\n", "auth", "login", "--username", "alice", "--profile", "prod", "--password-stdin")
	if code != ExitUsage || !strings.Contains(stderr, "changed target while login was in flight") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if content := readConfigContent(t, configPath); strings.Contains(content, "session-token") {
		t.Fatalf("session token was stored despite the in-flight target change: %s", content)
	}
	if fake.sawToken("session-token-1") {
		t.Fatalf("the issued token was used despite the refusal")
	}
}
