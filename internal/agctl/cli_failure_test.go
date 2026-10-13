package agctl

import (
	"strings"
	"testing"
)

func TestCLIUsageFailureOutputSelection(t *testing.T) {
	isolatedCLI(t)
	tests := []struct {
		name string
		args []string
		json bool
	}{
		{"unknown command trailing json", []string{"missing", "--json"}, true},
		{"unknown command trailing output", []string{"missing", "-o", "json"}, true},
		{"unknown command output equals", []string{"missing", "--o=json"}, true},
		{"unknown command explicit false", []string{"missing", "--json=false"}, false},
		{"unknown command repeated false", []string{"--json", "missing", "--json=false"}, false},
		{"unknown command repeated true", []string{"--json=false", "missing", "--json"}, true},
		{"unknown command output last wins", []string{"-o", "json", "missing", "-o", "table"}, false},
		{"unknown command json overrides table", []string{"missing", "--json", "-o", "table"}, true},
		{"unknown command false leaves output json", []string{"missing", "-o", "json", "--json=false"}, true},
		{"global unknown flag trailing json", []string{"--missing", "--json", "version"}, true},
		{"global unknown flag trailing output", []string{"--missing", "-o", "json", "version"}, true},
		{"local unknown flag trailing json", []string{"version", "--missing", "--json"}, true},
		{"local unknown flag trailing output", []string{"version", "--missing", "-o=json"}, true},
		{"local unknown flag repeated false", []string{"--json", "version", "--missing", "--json=false"}, false},
		{"invalid duration global", []string{"--timeout", "invalid", "--json", "version"}, true},
		{"invalid duration local", []string{"version", "--timeout=invalid", "--json"}, true},
		{"invalid boolean global", []string{"--insecure=invalid", "-o", "json", "version"}, true},
		{"invalid boolean local", []string{"version", "--verbose=invalid", "-o", "json"}, true},
		{"invalid json later corrected", []string{"version", "--json=invalid", "--json=true"}, true},
		{"invalid json preserves preceding true", []string{"--json", "version", "--json=invalid"}, true},
		{"missing value global", []string{"--json", "--timeout"}, true},
		{"missing value local", []string{"version", "-o", "json", "--server"}, true},
		{"missing output value", []string{"version", "--json", "-o"}, true},
		{"invalid output json override", []string{"version", "-o", "invalid", "--timeout=invalid", "--json"}, true},
		{"token value is not json flag", []string{"missing", "--token", "--json"}, false},
		{"server value is not json flag", []string{"missing", "--server", "--json"}, false},
		{"profile value is not output flag", []string{"missing", "--profile", "-o=json"}, false},
		{"duration value is not json flag", []string{"version", "--timeout", "--json"}, false},
		{"output value is not json flag", []string{"version", "-o", "--json"}, false},
		{"token consumes json false", []string{"--json", "missing", "--token", "--json=false"}, true},
		{"double dash stops unknown command scan", []string{"missing", "--", "--json"}, false},
		{"double dash stops failed global scan", []string{"--missing", "--", "--json"}, false},
		{"double dash stops failed local scan", []string{"version", "--missing", "--", "--json"}, false},
		{"double dash preserves earlier json", []string{"--json", "missing", "--", "--json=false"}, true},
		{"double dash used as token value", []string{"missing", "--token", "--", "--json"}, true},
		{"positional argument stops local scan", []string{"version", "extra", "--json"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, test.args...)
			if code != ExitUsage {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
			}
			if test.json {
				if stderr != "" {
					t.Fatalf("stderr = %q", stderr)
				}
				result := decodeEnvelope(t, stdout)
				if result.SchemaVersion != 1 || result.OK || result.Error == nil || result.Error.Code != "usage_error" {
					t.Fatalf("envelope = %#v", result)
				}
			} else if stdout != "" || !strings.Contains(stderr, "error:") {
				t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
			}
		})
	}
}

func TestCLIUsageFailureRedactsKnownTokens(t *testing.T) {
	isolatedCLI(t)
	const secret = "cli-failure-secret"
	tests := []struct {
		name string
		args []string
		env  bool
	}{
		{"flag token before invalid duration", []string{"version", "--token", secret, "--timeout", secret, "--json"}, false},
		{"flag token after invalid duration", []string{"version", "--timeout", secret, "--token", secret, "--json"}, false},
		{"flag token with equals", []string{"version", "--timeout", secret, "--token=" + secret}, false},
		{"overridden token still protected", []string{"version", "--token", secret, "--token", "other-token", "--timeout", secret, "--json"}, false},
		{"environment token protected", []string{"version", "--timeout", secret, "--json"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.env {
				t.Setenv("AGCTL_TOKEN", secret)
			}
			code, stdout, stderr := runCLI(t, test.args...)
			output := stdout + stderr
			if code != ExitUsage || strings.Contains(output, secret) || !strings.Contains(output, "REDACTED") {
				t.Fatalf("code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
			}
		})
	}
}

func TestCLISuccessfulOutputFlagPrecedenceIsUnchanged(t *testing.T) {
	isolatedCLI(t)
	tests := []struct {
		args []string
		json bool
	}{
		{[]string{"--json", "version", "--json=false"}, false},
		{[]string{"-o", "json", "version", "-o", "table"}, false},
		{[]string{"version", "--json=false", "-o", "json"}, true},
		{[]string{"version", "--json", "-o", "table"}, true},
		{[]string{"--", "version", "--json"}, true},
	}
	for _, test := range tests {
		code, stdout, stderr := runCLI(t, test.args...)
		if code != ExitOK || stderr != "" {
			t.Fatalf("%v: code = %d, stdout = %q, stderr = %q", test.args, code, stdout, stderr)
		}
		if test.json {
			if !decodeEnvelope(t, stdout).OK {
				t.Fatalf("%v: stdout = %q", test.args, stdout)
			}
		} else if !strings.HasPrefix(stdout, "agctl ") {
			t.Fatalf("%v: stdout = %q", test.args, stdout)
		}
	}
}
