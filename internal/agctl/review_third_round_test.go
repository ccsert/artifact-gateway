package agctl

import (
	"fmt"
	"strings"
	"testing"
)

func TestProfileIPv6TargetsDoNotShareCredentials(t *testing.T) {
	isolatedCLI(t)
	for _, pair := range [][2]string{
		{"http://[::1]:8080", "http://[::1:8080]"},
		{"https://[::1]:443", "https://[::1:443]"},
	} {
		configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: synthetic-old-token\n", pair[0]))
		code, stdout, stderr := runCLI(t, "config", "profile", "add", "prod", "--server", pair[1], "--json")
		if code != ExitOK || stderr != "" {
			t.Fatalf("update code=%d stderr=%q", code, stderr)
		}
		if strings.Contains(readConfigContent(t, configPath), "synthetic-old-token") {
			t.Fatal("different IPv6 authority retained the old credential")
		}
		if !strings.Contains(stdout, `"tokenCleared": true`) {
			t.Fatalf("update did not report the cleared credential: %s", stdout)
		}
	}
}

func TestAuthServerOverridesDoNotBorrowProfileToken(t *testing.T) {
	isolatedCLI(t)
	a := newAuthFakeGateway(t)
	for _, command := range []string{"whoami", "logout", "change-password"} {
		for _, override := range []string{"flag", "env"} {
			t.Run(command+"/"+override, func(t *testing.T) {
				b := newAuthFakeGateway(t)
				writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: synthetic-A-token\n", a.server.URL))
				args := []string{"auth", command, "--profile", "prod", "--json"}
				if override == "flag" {
					args = append(args, "--server", b.server.URL)
				} else {
					t.Setenv("AGCTL_SERVER", b.server.URL)
				}
				if command == "change-password" {
					args = append(args, "--password-stdin")
				}
				code, _, _ := runCLIStdin(t, "old-password\nnew-password\n", args...)
				if b.sawToken("synthetic-A-token") {
					t.Fatal("the overridden target received the old profile token")
				}
				if code != ExitUnauthenticated {
					t.Fatalf("code=%d, want missing credentials", code)
				}
			})
		}
	}
	// Explicit credentials belong to the caller's explicitly chosen target.
	for _, source := range []string{"flag", "env"} {
		t.Run("new-token/"+source, func(t *testing.T) {
			b := newAuthFakeGateway(t)
			b.valid["synthetic-B-token"] = true
			writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: synthetic-A-token\n", a.server.URL))
			args := []string{"auth", "whoami", "--profile", "prod", "--server", b.server.URL, "--json"}
			if source == "flag" {
				args = append(args, "--token", "synthetic-B-token")
			} else {
				t.Setenv("AGCTL_TOKEN", "synthetic-B-token")
			}
			if code, _, _ := runCLI(t, args...); code != ExitOK {
				t.Fatalf("explicit new-target token code=%d", code)
			}
		})
	}
}

func TestProfileAndLoginOutputsHideServerSecrets(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	server := strings.Replace(fake.server.URL, "://", "://private-user:private-password@", 1) + "?token=query-secret#fragment-secret"
	for _, format := range []string{"table", "json"} {
		for _, args := range [][]string{
			{"config", "profile", "add", "prod", "--server", server},
			{"config", "profile", "list"},
			{"config", "profile", "use", "prod"},
			{"auth", "login", "--profile", "prod", "--username", "alice", "--password-stdin"},
		} {
			code, stdout, stderr := runCLIStdin(t, fake.password+"\n", append(args, "-o", format)...)
			if code != ExitOK {
				t.Fatalf("%v: code=%d stderr=%q", args, code, stderr)
			}
			for _, secret := range []string{"private-user", "private-password", "query-secret", "fragment-secret", fake.password} {
				if strings.Contains(stdout+stderr, secret) {
					t.Fatalf("%v: %s output contains a server credential", args[:3], format)
				}
			}
		}
	}
}

func TestProfileTargetIdentityBoundaries(t *testing.T) {
	isolatedCLI(t)
	for _, test := range []struct {
		name, old, next string
		keep            bool
	}{
		{"ipv6 default port", "https://[::1]:443/", "https://[::1]", true},
		{"ipv6 spelling", "http://[FE80::1%25en0]:80", "http://[fe80::1%25en0]/", true},
		{"ipv6 zone case", "http://[fe80::1%25en0]", "http://[fe80::1%25EN0]", false},
		{"host and default port", "https://Gateway.Example:443/base/", "https://gateway.example/base", true},
		{"path case", "https://gateway.example/Base", "https://gateway.example/base", false},
		{"scheme", "https://gateway.example", "http://gateway.example", false},
		{"nondefault port", "https://gateway.example:8443", "https://gateway.example", false},
		{"encoded path", "https://gateway.example/base%2Fpath", "https://gateway.example/base/path", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: synthetic-bound-token\n", test.old))
			code, _, stderr := runCLI(t, "config", "profile", "add", "prod", "--server", test.next, "--json")
			if code != ExitOK {
				t.Fatalf("code=%d stderr=%q", code, stderr)
			}
			if kept := strings.Contains(readConfigContent(t, path), "synthetic-bound-token"); kept != test.keep {
				t.Fatalf("credential retained=%t, want %t", kept, test.keep)
			}
		})
	}
}

func TestProfileInvalidServerErrorHidesSecrets(t *testing.T) {
	isolatedCLI(t)
	for _, format := range []string{"table", "json"} {
		code, stdout, stderr := runCLI(t, "config", "profile", "add", "prod", "--server", "http://private-user:private-password@[::1?token=query-secret", "-o", format)
		if code != ExitUsage {
			t.Fatalf("code=%d", code)
		}
		for _, secret := range []string{"private-user", "private-password", "query-secret"} {
			if strings.Contains(stdout+stderr, secret) {
				t.Fatal("invalid server error leaked credentials")
			}
		}
	}
}

func TestProfileListHidesMalformedStoredServer(t *testing.T) {
	isolatedCLI(t)
	writeHomeConfig(t, "profiles:\n  prod:\n    server: https:user:synthetic-password@example.test\n")
	for _, format := range []string{"table", "json"} {
		code, stdout, stderr := runCLI(t, "config", "profile", "list", "-o", format)
		if code != ExitOK || strings.Contains(stdout+stderr, "synthetic-password") {
			t.Fatalf("malformed stored URL code=%d or leaked its credential", code)
		}
	}
}
