package agctl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type decodedEnvelope struct {
	SchemaVersion int             `json:"schemaVersion"`
	OK            bool            `json:"ok"`
	Data          json.RawMessage `json:"data"`
	Error         *struct {
		Code       string `json:"code"`
		HTTPStatus *int   `json:"httpStatus"`
		Message    string `json:"message"`
		Hint       string `json:"hint"`
		RequestID  string `json:"requestId"`
	} `json:"error"`
	Meta json.RawMessage `json:"meta"`
}

type decodedCommandTree struct {
	Items []struct {
		Name  string `json:"name"`
		Usage string `json:"usage"`
		Flags []struct {
			Name  string `json:"name"`
			Scope string `json:"scope"`
		} `json:"flags"`
		Meta struct {
			Resource  string   `json:"resource"`
			Scenarios []string `json:"scenarios"`
			Stage     string   `json:"stage"`
		} `json:"meta"`
	} `json:"items"`
	NextCursor *string `json:"nextCursor"`
}

type decodedInfoReport struct {
	Server string `json:"server"`
	Livez  struct {
		Status string `json:"status"`
	} `json:"livez"`
	Readyz struct {
		Status string `json:"status"`
	} `json:"readyz"`
	Identity *struct {
		Status        string `json:"status"`
		Actor         string `json:"actor"`
		Kind          string `json:"kind"`
		Role          string `json:"role"`
		Administrator *bool  `json:"administrator"`
		HTTPStatus    int    `json:"httpStatus"`
	} `json:"identity"`
	Formats *struct {
		Status  string   `json:"status"`
		Count   int      `json:"count"`
		Formats []string `json:"formats"`
	} `json:"formats"`
}

func isolatedCLI(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGCTL_SERVER", "")
	t.Setenv("AGCTL_TOKEN", "")
	t.Setenv("AGCTL_PROFILE", "")
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return runCLIStdin(t, "", args...)
}

func runCLIStdin(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := RunCLI(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func decodeEnvelope(t *testing.T, output string) decodedEnvelope {
	t.Helper()
	var envelope decodedEnvelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("stdout is not exactly one JSON document: %v\n%s", err, output)
	}
	return envelope
}

func writeProblem(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"type":"about:blank","title":%q,"status":%d,"code":%q,"message":%q,"requestId":"req-test"}`,
		http.StatusText(status), status, code, message)
}

type fakeGateway struct {
	server *httptest.Server
	token  string
}

func newFakeGateway(t *testing.T, token string) *fakeGateway {
	t.Helper()
	fake := &fakeGateway{token: token}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorized := fake.token != "" && r.Header.Get("Authorization") == "Bearer "+fake.token
		switch r.URL.Path {
		case "/livez", "/readyz":
			w.WriteHeader(http.StatusNoContent)
		case "/api/v2/identity":
			if !authorized {
				writeProblem(w, http.StatusUnauthorized, "access_denied", "authentication required")
				return
			}
			_, _ = fmt.Fprint(w, `{"actor":"svc-ci","kind":"service-account","role":"admin","administrator":true,"futureField":true}`)
		case "/api/v2/formats":
			if !authorized {
				writeProblem(w, http.StatusUnauthorized, "access_denied", "authentication required")
				return
			}
			_, _ = fmt.Fprint(w, `{"items":[{"format":"maven","repositoryTypes":["hosted"],"groupSupported":true,"anonymousRead":false,"hostedOperations":[],"proxyOperations":[]},{"format":"raw","repositoryTypes":["hosted","proxy"],"groupSupported":true,"anonymousRead":true,"hostedOperations":[],"proxyOperations":[]}]}`)
		default:
			writeProblem(w, http.StatusNotFound, "not_found", "no route")
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func TestVersionRunsInBothFormats(t *testing.T) {
	isolatedCLI(t)
	code, stdout, stderr := runCLI(t, "version")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "agctl ") || !strings.Contains(stdout, "(revision ") {
		t.Fatalf("stdout = %q", stdout)
	}

	code, stdout, _ = runCLI(t, "version", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	envelope := decodeEnvelope(t, stdout)
	if envelope.SchemaVersion != 1 || !envelope.OK || envelope.Error != nil {
		t.Fatalf("envelope = %#v", envelope)
	}
	var version struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(envelope.Data, &version); err != nil || version.Version == "" {
		t.Fatalf("data = %s, err = %v", envelope.Data, err)
	}

	if code, stdout, _ := runCLI(t, "--json", "-o", "json", "version"); code != ExitOK || decodeEnvelope(t, stdout).SchemaVersion != 1 {
		t.Fatalf("global flags before the command failed: code = %d", code)
	}
}

func TestJSONFlagFalseKeepsHumanOutput(t *testing.T) {
	isolatedCLI(t)
	code, stdout, _ := runCLI(t, "version", "--json=false")
	if code != ExitOK || strings.Contains(stdout, "{") {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
}

func TestCommandsPrintsTheTree(t *testing.T) {
	isolatedCLI(t)
	code, stdout, _ := runCLI(t, "commands")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	for _, name := range []string{"version", "info", "commands"} {
		if !strings.Contains(stdout, name) {
			t.Fatalf("stdout missing %q: %s", name, stdout)
		}
	}

	code, stdout, _ = runCLI(t, "commands", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	envelope := decodeEnvelope(t, stdout)
	if !envelope.OK {
		t.Fatalf("envelope = %#v", envelope)
	}
	var tree decodedCommandTree
	if err := json.Unmarshal(envelope.Data, &tree); err != nil {
		t.Fatalf("data: %v", err)
	}
	if len(tree.Items) != len(registry()) || tree.NextCursor != nil {
		t.Fatalf("tree = %#v", tree)
	}
	seen := map[string]bool{}
	for _, item := range tree.Items {
		seen[item.Name] = true
		if (item.Meta.Stage != "m0" && item.Meta.Stage != "m1") || item.Meta.Resource == "" {
			t.Fatalf("item %s meta = %#v", item.Name, item.Meta)
		}
		if len(item.Meta.Scenarios) == 0 || len(item.Meta.Scenarios) > 2 {
			t.Fatalf("item %s scenarios = %#v", item.Name, item.Meta.Scenarios)
		}
		for _, scenario := range item.Meta.Scenarios {
			if scenario != "ci" && scenario != "admin" {
				t.Fatalf("item %s scenario %q", item.Name, scenario)
			}
		}
		scopes := map[string]bool{}
		for _, flag := range item.Flags {
			scopes[flag.Name] = scopes[flag.Name] || flag.Scope == "global"
		}
		if !scopes["server"] || !scopes["json"] || !scopes["token"] {
			t.Fatalf("item %s flags = %#v", item.Name, item.Flags)
		}
	}
	for _, name := range []string{"version", "info", "commands"} {
		if !seen[name] {
			t.Fatalf("tree missing %q", name)
		}
	}
	var meta struct {
		Pagination struct {
			Mode string `json:"mode"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(envelope.Meta, &meta); err != nil || meta.Pagination.Mode != "none" {
		t.Fatalf("meta = %s, err = %v", envelope.Meta, err)
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	isolatedCLI(t)
	tests := []struct {
		name    string
		args    []string
		message string
	}{
		{"unknown command", []string{"badcmd"}, "unknown command"},
		{"unexpected argument", []string{"version", "extra"}, "unexpected argument"},
		{"non-positive timeout", []string{"version", "--timeout=0s"}, "must be positive"},
		{"invalid output format", []string{"version", "-o", "xml"}, "invalid output format"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, _, stderr := runCLI(t, test.args...)
			if code != ExitUsage || !strings.Contains(stderr, test.message) {
				t.Fatalf("code = %d, stderr = %q", code, stderr)
			}
		})
	}

	code, _, stderr := runCLI(t)
	if code != ExitUsage || !strings.Contains(stderr, "usage:") {
		t.Fatalf("bare invocation: code = %d, stderr = %q", code, stderr)
	}
}

func TestJSONFailureEnvelope(t *testing.T) {
	isolatedCLI(t)
	code, stdout, stderr := runCLI(t, "--json", "badcmd")
	if code != ExitUsage || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	envelope := decodeEnvelope(t, stdout)
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "usage_error" {
		t.Fatalf("envelope = %#v", envelope)
	}
	if !strings.Contains(envelope.Error.Message, "unknown command") || envelope.Error.HTTPStatus != nil {
		t.Fatalf("error = %#v", envelope.Error)
	}

	code, stdout, stderr = runCLI(t, "--json")
	bare := decodeEnvelope(t, stdout)
	if code != ExitUsage || stderr != "" || bare.Error == nil || !strings.Contains(bare.Error.Message, "no command given") {
		t.Fatalf("bare json invocation: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

func TestHelpPaths(t *testing.T) {
	isolatedCLI(t)
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		code, stdout, stderr := runCLI(t, args...)
		if code != ExitOK || !strings.Contains(stdout, "usage: agctl") || stderr != "" {
			t.Fatalf("%v: code = %d, stdout = %q, stderr = %q", args, code, stdout, stderr)
		}
	}
	for _, args := range [][]string{{"info", "--help"}, {"info", "-h"}} {
		code, stdout, _ := runCLI(t, args...)
		if code != ExitOK || !strings.Contains(stdout, "agctl info") || !strings.Contains(stdout, "-server") {
			t.Fatalf("%v: code = %d, stdout = %q", args, code, stdout)
		}
	}
}

func TestInfoRequiresServer(t *testing.T) {
	isolatedCLI(t)
	code, _, stderr := runCLI(t, "info")
	if code != ExitUsage || !strings.Contains(stderr, "no server configured") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	code, stdout, _ := runCLI(t, "info", "--json")
	if code != ExitUsage {
		t.Fatalf("code = %d", code)
	}
	if envelope := decodeEnvelope(t, stdout); envelope.OK || envelope.Error.Code != "usage_error" {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestInfoReportsIdentityAndFormats(t *testing.T) {
	isolatedCLI(t)
	fake := newFakeGateway(t, "secret-token")

	code, stdout, stderr := runCLI(t, "info", "--server", fake.server.URL, "--token", "secret-token")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"livez:", "ok", "readyz:", "svc-ci", "kind=service-account", "role=admin", "administrator=true", "2 formats: maven, raw"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}

	code, stdout, _ = runCLI(t, "info", "--server", fake.server.URL, "--token", "secret-token", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	envelope := decodeEnvelope(t, stdout)
	var report decodedInfoReport
	if err := json.Unmarshal(envelope.Data, &report); err != nil {
		t.Fatalf("data: %v", err)
	}
	if report.Livez.Status != "ok" || report.Readyz.Status != "ok" {
		t.Fatalf("report = %#v", report)
	}
	if report.Identity == nil || report.Identity.Status != "ok" || report.Identity.Actor != "svc-ci" || report.Identity.Administrator == nil || !*report.Identity.Administrator {
		t.Fatalf("identity = %#v", report.Identity)
	}
	if report.Formats == nil || report.Formats.Count != 2 || report.Formats.Formats[0] != "maven" {
		t.Fatalf("formats = %#v", report.Formats)
	}
}

func TestInfoWithoutCredentialsStaysInformative(t *testing.T) {
	isolatedCLI(t)
	fake := newFakeGateway(t, "secret-token")

	code, stdout, _ := runCLI(t, "info", "--server", fake.server.URL, "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var report decodedInfoReport
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &report); err != nil {
		t.Fatalf("data: %v", err)
	}
	if report.Identity == nil || report.Identity.Status != "unauthenticated" || report.Formats == nil || report.Formats.Status != "unauthenticated" {
		t.Fatalf("report = %#v", report)
	}
	if report.Livez.Status != "ok" {
		t.Fatalf("livez = %#v", report.Livez)
	}
}

func TestInfoReportsRejectedCredentialsWithoutFailing(t *testing.T) {
	isolatedCLI(t)
	fake := newFakeGateway(t, "secret-token")

	code, stdout, _ := runCLI(t, "info", "--server", fake.server.URL, "--token", "wrong-token", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var report decodedInfoReport
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &report); err != nil {
		t.Fatalf("data: %v", err)
	}
	if report.Identity == nil || report.Identity.Status != "unauthorized" || report.Identity.HTTPStatus != 401 {
		t.Fatalf("identity = %#v", report.Identity)
	}
	if report.Formats == nil || report.Formats.Status != "unauthorized" {
		t.Fatalf("formats = %#v", report.Formats)
	}
}

func TestInfoUnreachableServerExitsEight(t *testing.T) {
	isolatedCLI(t)
	code, _, stderr := runCLI(t, "info", "--server", "http://127.0.0.1:1")
	if code != ExitConnection || !strings.Contains(stderr, "error:") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}

	code, stdout, _ := runCLI(t, "info", "--server", "http://127.0.0.1:1", "--json")
	if code != ExitConnection {
		t.Fatalf("code = %d", code)
	}
	envelope := decodeEnvelope(t, stdout)
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "connection_error" {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestConfigFileDrivesTheInvocation(t *testing.T) {
	isolatedCLI(t)
	fake := newFakeGateway(t, "file-token")
	configDir := filepath.Join(os.Getenv("HOME"), ".config", "agctl")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("profiles:\n  default:\n    server: %s\n    token: file-token\n", fake.server.URL)
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runCLI(t, "info", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var report decodedInfoReport
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &report); err != nil || report.Identity.Status != "ok" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}

	// A flag still wins over the file: the unreachable override fails.
	code, _, _ = runCLI(t, "info", "--server", "http://127.0.0.1:1")
	if code != ExitConnection {
		t.Fatalf("flag override code = %d", code)
	}
}

func TestConfigFilePermissionsFailLoudly(t *testing.T) {
	isolatedCLI(t)
	configDir := filepath.Join(os.Getenv("HOME"), ".config", "agctl")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("profiles: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, "version")
	if code != ExitUsage || !strings.Contains(stderr, "chmod 600") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestVerboseOutputNeverPrintsCredentials(t *testing.T) {
	isolatedCLI(t)
	fake := newFakeGateway(t, "hunter2-secret")
	code, _, stderr := runCLI(t, "info", "--server", fake.server.URL, "--token", "hunter2-secret", "--verbose")
	if code != ExitOK {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "> GET") || !strings.Contains(stderr, "< 204") {
		t.Fatalf("stderr = %q", stderr)
	}
	if strings.Contains(stderr, "hunter2-secret") {
		t.Fatalf("verbose output leaked the token: %q", stderr)
	}
}

func TestEnvironmentDrivesTheInvocation(t *testing.T) {
	isolatedCLI(t)
	fake := newFakeGateway(t, "env-token")
	t.Setenv("AGCTL_SERVER", fake.server.URL)
	t.Setenv("AGCTL_TOKEN", "env-token")

	code, stdout, _ := runCLI(t, "info", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var report decodedInfoReport
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &report); err != nil || report.Identity.Status != "ok" {
		t.Fatalf("report = %#v, err = %v", report, err)
	}
}
