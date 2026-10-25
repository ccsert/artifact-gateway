package agctl

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func testShellSession(t *testing.T) *shellSession {
	t.Helper()
	isolatedCLI(t)
	return &shellSession{env: &commandContext{globals: &globalFlags{timeout: defaultTimeout}, flagSet: map[string]bool{}, configPath: defaultConfigPath()}}
}

func TestShellParsing(t *testing.T) {
	for _, tc := range []struct {
		line    string
		want    []string
		invalid bool
	}{
		{`artifact search --query "two words"`, []string{"artifact", "search", "--query", "two words"}, false},
		{`version --token='literal $()'`, []string{"version", "--token=literal $()"}, false},
		{`use repo escaped\ space`, []string{"use", "repo", "escaped space"}, false},
		{`user create ''`, []string{"user", "create", ""}, false},
		{`"unfinished`, nil, true}, {`trailing\`, nil, true},
	} {
		got, err := splitShellLine(tc.line)
		if (err != nil) != tc.invalid || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("parse %q: %q, %v", tc.line, got, err)
		}
	}
}

func TestShellContextAndStickyFlags(t *testing.T) {
	s := testShellSession(t)
	cfg := &configFile{CurrentProfile: "first", Profiles: map[string]profileConfig{"first": {Server: "https://first.example"}, "second": {Server: "https://second.example"}}}
	if err := writeConfigFile(s.env.configPath, cfg); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"use", "repo", "repo-one"}, {"set", "json", "on"}, {"set", "dry-run", "on"}, {"set", "verbose", "on"}} {
		if handled, exit, err := s.builtin(args); !handled || exit || err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, path := range [][]string{{"artifact", "list"}, {"--timeout=1s", "artifact", "list"}, {"repo", "list"}, {"version"}, {"commands"}, {"auth", "whoami"}} {
		args := s.arguments(path)
		joined := strings.Join(args, " ")
		wantRepo := strings.Contains(strings.Join(path, " "), "artifact list")
		if strings.Contains(joined, "--repo=repo-one") != wantRepo {
			t.Fatalf("incorrect repo injection: %v", args)
		}
		for _, flag := range []string{"--json=true", "--dry-run=true", "--verbose=true"} {
			if !strings.Contains(joined, flag) {
				t.Fatalf("missing %s: %v", flag, args)
			}
		}
	}
	if _, _, err := s.builtin([]string{"use", "profile", "second"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfigFile(s.env.configPath)
	if err != nil || loaded.CurrentProfile != "first" || s.profile != "second" || s.repo != "" {
		t.Fatalf("profile switch mutated storage or kept repo: %#v, %v", loaded, err)
	}
	if prompt := s.prompt(); !strings.Contains(prompt, "second | https://second.example") {
		t.Fatal(prompt)
	}
	for _, option := range []string{"json", "dry-run", "verbose"} {
		_, _, err := s.builtin([]string{"set", option, "off"})
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.json || s.dryRun || s.verbose {
		t.Fatal("sticky flags did not turn off")
	}
	for _, args := range [][]string{{"use"}, {"use", "profile", "missing"}, {"set", "json", "maybe"}, {"set", "yes", "on"}, {"exit", "extra"}, {"--json", "shell"}} {
		if handled, _, err := s.builtin(args); !handled || err == nil {
			t.Fatalf("must reject %v", args)
		}
	}
}

func TestShellHistoryFiltersSyntheticSecrets(t *testing.T) {
	s := testShellSession(t)
	path := filepath.Join(filepath.Dir(s.env.configPath), "history")
	secret := "SYNTHETIC_SECRET_136"
	for _, line := range []string{"version --token " + secret, "user create '" + secret + "' --password-file '/private/" + secret + "'", "email target create x --recipient-file " + secret, "repo create x --description '{\"body\":\"" + secret + "\"}'", "credential issue account --secret-output " + secret, secret, "use repo " + secret, "--token=" + secret + " artifact list --repo " + secret} {
		if err := appendShellHistory(path, shellHistoryEntry(line)); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(string(data), "--") {
		t.Fatalf("history leaked private input: %s", data)
	}
	entries, err := readShellHistory(path)
	if err != nil || len(entries) != 6 {
		t.Fatalf("history entries %q, %v", entries, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readShellHistory(path); err == nil {
		t.Fatal("accepted public history")
	}
	if err := appendShellHistory(path, "version"); err == nil {
		t.Fatal("overwrote public history")
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "history")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := appendShellHistory(link, "version"); err == nil {
		t.Fatal("accepted symlink history")
	}
	original, _ := os.ReadFile(target)
	if string(original) != secret {
		t.Fatal("modified symlink target")
	}
}

func TestShellCompletionUsesAuthorizedRepositoryList(t *testing.T) {
	s := testShellSession(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer selected-token" || r.URL.Path != repositoriesPath {
			t.Errorf("unexpected completion request %s", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		if r.URL.Query().Get("pageToken") == "second" {
			_, _ = fmt.Fprint(w, `{"items":[{"id":"repo-last","name":"last","format":"raw","state":"active"},{"id":"repo-\u001b[31m","name":"unsafe","format":"raw","state":"active"}]}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"items":[{"id":"repo-visible","name":"visible","format":"raw","state":"active"}],"nextPageToken":"second"}`)
	}))
	defer server.Close()
	if err := writeConfigFile(s.env.configPath, &configFile{Profiles: map[string]profileConfig{"prod": {Server: server.URL, Token: "selected-token"}}}); err != nil {
		t.Fatal(err)
	}
	s.profile = "prod"
	for _, tc := range []struct {
		line string
		want []string
	}{{"use profile p", []string{"prod"}}, {"use repo repo-", []string{"repo-last", "repo-visible"}}, {"artifact l", []string{"list"}}, {"set j", []string{"json"}}} {
		if got := s.candidates(context.Background(), tc.line); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%q: %v", tc.line, got)
		}
	}
}

func TestShellStickyOptionsPreserveWriteSafety(t *testing.T) {
	s := testShellSession(t)
	var methods []string
	var methodMu sync.Mutex
	recorded := func() string { methodMu.Lock(); defer methodMu.Unlock(); return strings.Join(methods, ",") }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methodMu.Lock()
		methods = append(methods, r.Method)
		methodMu.Unlock()
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `{"id":"target","name":"target","format":"raw","state":"active","version":"v1"}`)
			return
		}
		writeProblem(w, http.StatusForbidden, "access_denied", "administrator permission required")
	}))
	defer server.Close()
	s.env.globals.server = server.URL
	s.env.flagSet["server"] = true
	s.env.globals.token = "synthetic-token"
	s.env.flagSet["token"] = true
	// Even a startup --yes is not a sticky authorization to delete later.
	s.env.globals.yes = true
	s.env.flagSet["yes"] = true
	s.json = true
	s.repo = "unrelated-context"
	run := func(args ...string) (int, decodedEnvelope) {
		var out, errout bytes.Buffer
		code := RunCLI(context.Background(), s.arguments(args), strings.NewReader(""), &out, &errout)
		return code, decodeEnvelope(t, out.String())
	}
	if code, _ := run("repo", "delete", "target"); code != ExitUsage || recorded() != "" {
		t.Fatalf("confirmation bypass: code %d, methods %v", code, recorded())
	}
	s.dryRun = true
	if code, env := run("repo", "delete", "target"); code != ExitOK || !env.OK || recorded() != "GET" {
		t.Fatalf("dry-run submitted a write: code %d, methods %v", code, recorded())
	}
	s.dryRun = false
	if code, env := run("repo", "delete", "target", "--yes"); code != ExitForbidden || env.OK || env.Error == nil || env.Error.HTTPStatus == nil || *env.Error.HTTPStatus != 403 {
		t.Fatalf("authorization bypass: code %d, envelope %+v", code, env)
	}
	if recorded() != "GET,DELETE" {
		t.Fatal(recorded())
	}
}

func TestShellRunCLIParityAndInputIsolation(t *testing.T) {
	s := testShellSession(t)
	s.json = true
	s.repo = "context-repo"
	for _, args := range [][]string{{"version"}, {"commands"}, {"auth", "login", "--username", "someone", "--password-stdin"}, {"config", "profile", "add", "x", "--server", "https://example.com", "--token-stdin"}} {
		var actual, fail bytes.Buffer
		got := RunCLI(context.Background(), s.arguments(args), strings.NewReader(""), &actual, &fail)
		code, out, errout := runCLI(t, append([]string{"--json"}, args...)...)
		if got != code || actual.String() != out || fail.String() != errout {
			t.Fatalf("parity failed for %v: %d/%d, %s/%s", args, got, code, actual.String(), out)
		}
	}
	// Repository context is only a default; an explicit command flag wins.
	s.env.globals.server = "http://127.0.0.1:1"
	s.env.flagSet["server"] = true
	args := s.arguments([]string{"artifact", "list", "--repo=explicit", "--dry-run"})
	if strings.Index(strings.Join(args, " "), "--repo=context-repo") > strings.Index(strings.Join(args, " "), "--repo=explicit") {
		t.Fatal(args)
	}
	code, _, _ := runCLI(t, "shell")
	if code != ExitUsage {
		t.Fatalf("non-TTY exit %d", code)
	}
	// EOF readers are deliberate: private reads must return immediately.
	if _, err := readBoundedSecret(strings.NewReader(""), secretInputLimit, "password", ""); err == nil {
		t.Fatal("empty shell stdin accepted")
	}
}
