package agctl

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestConfigFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func staticEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestResolveSettingsPrecedence(t *testing.T) {
	path := writeTestConfigFile(t, `current_profile: production
profiles:
  production:
    server: https://file.example
    token: file-token
    insecure: true
`, 0o600)

	tests := []struct {
		name         string
		flags        *globalFlags
		set          map[string]bool
		env          map[string]string
		wantServer   string
		wantToken    string
		wantInsecure bool
	}{
		{
			name:         "flags win over everything",
			flags:        &globalFlags{server: "https://flag.example", token: "flag-token", profile: "production"},
			set:          map[string]bool{"server": true, "token": true},
			env:          map[string]string{"AGCTL_SERVER": "https://env.example", "AGCTL_TOKEN": "env-token"},
			wantServer:   "https://flag.example",
			wantToken:    "flag-token",
			wantInsecure: true,
		},
		{
			name:         "environment wins over the file",
			flags:        &globalFlags{},
			set:          map[string]bool{},
			env:          map[string]string{"AGCTL_SERVER": "https://env.example", "AGCTL_TOKEN": "env-token"},
			wantServer:   "https://env.example",
			wantToken:    "env-token",
			wantInsecure: true,
		},
		{
			name:         "file fills what flags and environment leave unset",
			flags:        &globalFlags{},
			set:          map[string]bool{},
			env:          map[string]string{},
			wantServer:   "https://file.example",
			wantToken:    "file-token",
			wantInsecure: true,
		},
		{
			name:         "explicit insecure=false overrides the profile",
			flags:        &globalFlags{insecure: false},
			set:          map[string]bool{"insecure": true},
			env:          map[string]string{},
			wantServer:   "https://file.example",
			wantToken:    "file-token",
			wantInsecure: false,
		},
		{
			name:         "empty environment values are unset",
			flags:        &globalFlags{},
			set:          map[string]bool{},
			env:          map[string]string{"AGCTL_SERVER": "", "AGCTL_TOKEN": ""},
			wantServer:   "https://file.example",
			wantToken:    "file-token",
			wantInsecure: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings, err := resolveSettings(test.flags, test.set, staticEnv(test.env), path)
			if err != nil {
				t.Fatalf("resolveSettings: %v", err)
			}
			if settings.Server != test.wantServer || settings.Token != test.wantToken || settings.Insecure != test.wantInsecure {
				t.Fatalf("settings = %#v", settings)
			}
		})
	}
}

func TestResolveSettingsProfileSelection(t *testing.T) {
	path := writeTestConfigFile(t, `profiles:
  alpha:
    server: https://alpha.example
    token: alpha-token
  beta:
    server: https://beta.example
    token: beta-token
`, 0o600)

	tests := []struct {
		name        string
		flags       *globalFlags
		set         map[string]bool
		env         map[string]string
		wantServer  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "flag selects the profile",
			flags:      &globalFlags{profile: "beta"},
			set:        map[string]bool{"profile": true},
			wantServer: "https://beta.example",
		},
		{
			name:       "environment selects the profile",
			flags:      &globalFlags{},
			set:        map[string]bool{},
			env:        map[string]string{"AGCTL_PROFILE": "alpha"},
			wantServer: "https://alpha.example",
		},
		{
			name:        "unknown explicit profile fails",
			flags:       &globalFlags{profile: "missing"},
			set:         map[string]bool{"profile": true},
			wantErr:     true,
			errContains: `profile "missing" not found`,
		},
		{
			name:       "no default profile leaves the settings empty",
			flags:      &globalFlags{},
			set:        map[string]bool{},
			wantServer: "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			settings, err := resolveSettings(test.flags, test.set, staticEnv(test.env), path)
			if test.wantErr {
				var usage *UsageError
				if !errors.As(err, &usage) || !strings.Contains(usage.Message, test.errContains) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveSettings: %v", err)
			}
			if settings.Server != test.wantServer {
				t.Fatalf("server = %q", settings.Server)
			}
		})
	}
}

func TestResolveSettingsUsesCurrentProfileAndSingleProfile(t *testing.T) {
	current := writeTestConfigFile(t, `current_profile: staging
profiles:
  staging:
    server: https://staging.example
    token: staging-token
`, 0o600)
	settings, err := resolveSettings(&globalFlags{}, map[string]bool{}, staticEnv(nil), current)
	if err != nil || settings.Server != "https://staging.example" || settings.Name != "staging" {
		t.Fatalf("settings = %#v, err = %v", settings, err)
	}

	single := writeTestConfigFile(t, `profiles:
  only:
    server: https://only.example
`, 0o600)
	settings, err = resolveSettings(&globalFlags{}, map[string]bool{}, staticEnv(nil), single)
	if err != nil || settings.Server != "https://only.example" || settings.Name != "only" {
		t.Fatalf("settings = %#v, err = %v", settings, err)
	}
}

func TestResolveSettingsFailsOnDanglingCurrentProfile(t *testing.T) {
	path := writeTestConfigFile(t, `current_profile: gone
profiles:
  alpha:
    server: https://alpha.example
`, 0o600)
	_, err := resolveSettings(&globalFlags{}, map[string]bool{}, staticEnv(nil), path)
	var usage *UsageError
	if !errors.As(err, &usage) || !strings.Contains(usage.Message, "current_profile") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveSettingsRejectsReadableConfigFile(t *testing.T) {
	path := writeTestConfigFile(t, "profiles: {}\n", 0o644)
	_, err := resolveSettings(&globalFlags{}, map[string]bool{}, staticEnv(nil), path)
	var usage *UsageError
	if !errors.As(err, &usage) || !strings.Contains(usage.Hint, "chmod 600") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveSettingsHandlesMissingAndInvalidFiles(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent", "config.yaml")
	settings, err := resolveSettings(&globalFlags{}, map[string]bool{}, staticEnv(nil), missing)
	if err != nil || settings.Server != "" {
		t.Fatalf("missing file: settings = %#v, err = %v", settings, err)
	}

	_, err = resolveSettings(&globalFlags{profile: "x"}, map[string]bool{"profile": true}, staticEnv(nil), missing)
	var usage *UsageError
	if !errors.As(err, &usage) || !strings.Contains(usage.Message, "does not exist") {
		t.Fatalf("explicit profile without file: err = %v", err)
	}

	invalid := writeTestConfigFile(t, "profiles: [", 0o600)
	_, err = resolveSettings(&globalFlags{}, map[string]bool{}, staticEnv(nil), invalid)
	if !errors.As(err, &usage) || !strings.Contains(usage.Message, "not valid YAML") {
		t.Fatalf("invalid YAML: err = %v", err)
	}
}

func TestResolveSettingsWithoutConfigPath(t *testing.T) {
	settings, err := resolveSettings(&globalFlags{}, map[string]bool{}, staticEnv(nil), "")
	if err != nil || settings.Server != "" {
		t.Fatalf("settings = %#v, err = %v", settings, err)
	}
}

func TestExitCodeFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, ExitOK},
		{"usage", &UsageError{Message: "bad"}, ExitUsage},
		{"credentials", &CredentialsError{Message: "missing"}, ExitUnauthenticated},
		{"partial", &PartialError{Message: "some"}, ExitPartial},
		{"not found", &APIError{HTTPStatus: 404}, ExitNotFound},
		{"unauthenticated", &APIError{HTTPStatus: 401}, ExitUnauthenticated},
		{"forbidden", &APIError{HTTPStatus: 403}, ExitForbidden},
		{"conflict", &APIError{HTTPStatus: 409}, ExitConflict},
		{"precondition", &APIError{HTTPStatus: 412}, ExitConflict},
		{"server failure", &APIError{HTTPStatus: 500}, ExitFailure},
		{"connection", &ConnectionError{Message: "down"}, ExitConnection},
		{"unknown", errors.New("boom"), ExitFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := exitCodeFor(test.err); got != test.want {
				t.Fatalf("exitCodeFor = %d, want %d", got, test.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	if wait, ok := parseRetryAfter("3", now); !ok || wait != 3*time.Second {
		t.Fatalf("seconds form: wait = %s, ok = %t", wait, ok)
	}
	if wait, ok := parseRetryAfter("", now); ok || wait != 0 {
		t.Fatalf("empty: wait = %s, ok = %t", wait, ok)
	}
	if wait, ok := parseRetryAfter("not-a-date", now); ok {
		t.Fatalf("invalid: wait = %s, ok = %t", wait, ok)
	}
	httpDate := now.Add(2 * time.Second).UTC().Format(http.TimeFormat)
	if wait, ok := parseRetryAfter(httpDate, now); !ok || wait != 2*time.Second {
		t.Fatalf("http date: wait = %s, ok = %t", wait, ok)
	}
	if wait, ok := parseRetryAfter(now.Add(-time.Minute).UTC().Format(http.TimeFormat), now); !ok || wait != 0 {
		t.Fatalf("past date: wait = %s, ok = %t", wait, ok)
	}
}
