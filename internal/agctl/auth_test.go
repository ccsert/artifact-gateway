package agctl

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// authFakeGateway models the contract-external auth endpoints used by the CLI:
// local login, session logout, own-password change, plus the identity and
// repository reads for whoami.
type authFakeGateway struct {
	server *httptest.Server

	mu                  sync.Mutex
	password            string
	mustChange          bool
	valid               map[string]bool
	sessions            map[string]bool
	loginCalls          int
	logoutCall          int
	changeBody          map[string]string
	lastLogin           map[string]string
	authorizations      []string
	beforeLoginResponse func()
}

func newAuthFakeGateway(t *testing.T) *authFakeGateway {
	t.Helper()
	fake := &authFakeGateway{
		password: "initial-password",
		valid:    map[string]bool{},
		sessions: map[string]bool{},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		fake.authorizations = append(fake.authorizations, r.Header.Get("Authorization"))
		hook := fake.beforeLoginResponse
		fake.mu.Unlock()
		if r.URL.Path == "/auth/login" && hook != nil {
			hook()
		}
		switch r.URL.Path {
		case "/auth/login":
			fake.handleLogin(w, r)
		case "/auth/logout":
			fake.mu.Lock()
			fake.logoutCall++
			token := bearerToken(r)
			// Like the real Gateway, only local user sessions are revoked;
			// machine credentials and static tokens keep answering.
			if token != "" && fake.sessions[token] {
				delete(fake.valid, token)
			}
			fake.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "/auth/session":
			fake.mu.Lock()
			authenticated := fake.valid[bearerToken(r)]
			fake.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": authenticated})
		case "/auth/change-password":
			fake.handleChangePassword(w, r)
		case "/api/v2/identity":
			if !fake.authorized(r) {
				writeProblem(w, http.StatusUnauthorized, "access_denied", "authentication required")
				return
			}
			_, _ = fmt.Fprint(w, `{"actor":"user:alice","kind":"local","role":"member","administrator":false}`)
		case "/api/v2/repositories":
			if !fake.authorized(r) {
				writeProblem(w, http.StatusUnauthorized, "access_denied", "authentication required")
				return
			}
			_, _ = fmt.Fprint(w, `{"items":[{"name":"alpha","format":"raw"},{"name":"beta","format":"maven"}]}`)
		default:
			writeProblem(w, http.StatusNotFound, "not_found", "no route")
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func bearerToken(r *http.Request) string {
	value := r.Header.Get("Authorization")
	return strings.TrimPrefix(value, "Bearer ")
}

func (f *authFakeGateway) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loginCalls++
	f.lastLogin = map[string]string{"username": body.Username, "password": body.Password}
	if body.Username == "" || body.Password != f.password {
		writeProblem(w, http.StatusUnauthorized, "access_denied", "invalid credentials")
		return
	}
	token := fmt.Sprintf("session-token-%d", f.loginCalls)
	f.valid[token] = true
	f.sessions[token] = true
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":              token,
		"username":           body.Username,
		"displayName":        "Alice",
		"role":               "member",
		"mustChangePassword": f.mustChange,
	})
}

func (f *authFakeGateway) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) {
		writeProblem(w, http.StatusUnauthorized, "access_denied", "a local user session is required")
		return
	}
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "unreadable body")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changeBody = map[string]string{"currentPassword": body.CurrentPassword, "newPassword": body.NewPassword}
	if body.CurrentPassword != f.password {
		writeProblem(w, http.StatusUnauthorized, "access_denied", "current password is invalid")
		return
	}
	f.password = body.NewPassword
	w.WriteHeader(http.StatusNoContent)
}

func (f *authFakeGateway) authorized(r *http.Request) bool {
	token := bearerToken(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	return token != "" && f.valid[token]
}

func (f *authFakeGateway) tokenIsValid(token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.valid[token]
}

// sawToken reports whether any request carried the token as a bearer header.
func (f *authFakeGateway) sawToken(token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, header := range f.authorizations {
		if header == "Bearer "+token {
			return true
		}
	}
	return false
}

func (f *authFakeGateway) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.authorizations)
}

// writeHomeConfig writes ~/.config/agctl/config.yaml under the isolated HOME
// and returns its path.
func writeHomeConfig(t *testing.T, content string) string {
	t.Helper()
	directory := filepath.Join(os.Getenv("HOME"), ".config", "agctl")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readConfigContent(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestAuthLoginStoresSessionToken(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n", fake.server.URL))

	code, stdout, stderr := runCLIStdin(t, fake.password+"\n", "auth", "login", "--username", "alice", "--profile", "prod", "--password-stdin", "--json")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	envelope := decodeEnvelope(t, stdout)
	if !envelope.OK {
		t.Fatalf("envelope = %#v", envelope)
	}
	var data authLoginResult
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatalf("data: %v", err)
	}
	if data.Profile != "prod" || data.Username != "alice" || data.MustChangePassword {
		t.Fatalf("data = %#v", data)
	}
	if strings.Contains(stdout, "session-token") {
		t.Fatalf("stdout leaked the session token: %s", stdout)
	}
	if fake.loginCalls != 1 || fake.lastLogin["username"] != "alice" || fake.lastLogin["password"] != fake.password {
		t.Fatalf("login call = %#v", fake.lastLogin)
	}
	content := readConfigContent(t, configPath)
	if !strings.Contains(content, "session-token-1") {
		t.Fatalf("config missing the stored token: %s", content)
	}
	info, err := os.Stat(configPath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v, err = %v", info.Mode(), err)
	}
}

func TestAuthLoginWrongPasswordExitsFive(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n", fake.server.URL))

	code, stdout, stderr := runCLIStdin(t, "wrong-password\n", "auth", "login", "--username", "alice", "--profile", "prod", "--password-stdin", "--json")
	if code != ExitUnauthenticated {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	envelope := decodeEnvelope(t, stdout)
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "access_denied" || envelope.Error.HTTPStatus == nil || *envelope.Error.HTTPStatus != 401 {
		t.Fatalf("envelope = %#v", envelope)
	}
	if strings.Contains(stdout, "wrong-password") || strings.Contains(stderr, "wrong-password") {
		t.Fatalf("password leaked: stdout=%q stderr=%q", stdout, stderr)
	}
	if content := readConfigContent(t, configPath); strings.Contains(content, "token") {
		t.Fatalf("config stored a token after a failed login: %s", content)
	}
}

func TestAuthLoginRequiresExplicitProfile(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	writeHomeConfig(t, fmt.Sprintf("current_profile: prod\nprofiles:\n  prod:\n    server: %s\n", fake.server.URL))

	code, _, stderr := runCLIStdin(t, fake.password+"\n", "auth", "login", "--username", "alice", "--password-stdin")
	if code != ExitUsage || !strings.Contains(stderr, "explicit profile") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if fake.loginCalls != 0 {
		t.Fatalf("login was called %d times without an explicit profile", fake.loginCalls)
	}
}

func TestAuthLoginRequiresBoundedPasswordInput(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n", fake.server.URL))

	code, _, stderr := runCLI(t, "auth", "login", "--username", "alice", "--profile", "prod")
	if code != ExitUsage || !strings.Contains(stderr, "--password-stdin") {
		t.Fatalf("missing flag: code = %d, stderr = %q", code, stderr)
	}
	code, _, stderr = runCLIStdin(t, "", "auth", "login", "--username", "alice", "--profile", "prod", "--password-stdin")
	if code != ExitUsage || !strings.Contains(stderr, "no password provided on stdin") {
		t.Fatalf("empty stdin: code = %d, stderr = %q", code, stderr)
	}
	code, _, stderr = runCLIStdin(t, strings.Repeat("x", secretInputLimit+10)+"\n", "auth", "login", "--username", "alice", "--profile", "prod", "--password-stdin")
	if code != ExitUsage || !strings.Contains(stderr, "exceeds") {
		t.Fatalf("oversized stdin: code = %d, stderr = %q", code, stderr)
	}
	if fake.loginCalls != 0 {
		t.Fatalf("login was called %d times with invalid input", fake.loginCalls)
	}
}

func TestAuthLoginGuidesThroughRequiredPasswordChange(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	fake.mustChange = true
	writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n", fake.server.URL))

	code, stdout, _ := runCLIStdin(t, fake.password+"\n", "auth", "login", "--username", "alice", "--profile", "prod", "--password-stdin")
	if code != ExitOK || !strings.Contains(stdout, "change-password") {
		t.Fatalf("code = %d, stdout = %q", code, stdout)
	}
	code, stdout, _ = runCLIStdin(t, fake.password+"\n", "auth", "login", "--username", "alice", "--profile", "prod", "--password-stdin", "--json")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	var data authLoginResult
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &data); err != nil || !data.MustChangePassword {
		t.Fatalf("data = %#v, err = %v", data, err)
	}
}

func TestAuthWhoamiShowsIdentityAndRepositories(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	fake.valid["stored-token"] = true
	writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: stored-token\n", fake.server.URL))

	code, stdout, stderr := runCLI(t, "auth", "whoami", "--json")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var data whoamiResult
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &data); err != nil {
		t.Fatalf("data: %v", err)
	}
	if data.Actor != "user:alice" || data.Kind != "local" || data.Role != "member" || data.Administrator {
		t.Fatalf("identity = %#v", data)
	}
	if data.Repositories.Count != 2 || len(data.Repositories.Names) != 2 || data.Repositories.Names[0] != "alpha" || data.Repositories.Truncated {
		t.Fatalf("repositories = %#v", data.Repositories)
	}

	code, stdout, _ = runCLI(t, "auth", "whoami")
	if code != ExitOK || !strings.Contains(stdout, "identity: user:alice") || !strings.Contains(stdout, "repositories: 2 visible: alpha, beta") {
		t.Fatalf("human output: code = %d, stdout = %q", code, stdout)
	}
}

func TestAuthWhoamiWithoutCredentialsExitsFive(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n", fake.server.URL))

	code, _, stderr := runCLI(t, "auth", "whoami")
	if code != ExitUnauthenticated || !strings.Contains(stderr, "no credentials") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestAuthLogoutRevokesAndClearsStoredToken(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	fake.valid["stored-token"] = true
	fake.sessions["stored-token"] = true
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: stored-token\n", fake.server.URL))

	code, stdout, stderr := runCLI(t, "auth", "logout", "--json")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var data authLogoutResult
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &data); err != nil {
		t.Fatalf("data: %v", err)
	}
	if !data.ServerLogoutRequested || data.AlreadyInvalid || data.ProfileCleared != "prod" {
		t.Fatalf("data = %#v", data)
	}
	if data.CredentialInvalidated == nil || !*data.CredentialInvalidated {
		t.Fatalf("credential state = %#v", data.CredentialInvalidated)
	}
	if fake.logoutCall != 1 || fake.tokenIsValid("stored-token") {
		t.Fatalf("server state: logoutCall = %d, still valid = %t", fake.logoutCall, fake.tokenIsValid("stored-token"))
	}
	if content := readConfigContent(t, configPath); strings.Contains(content, "stored-token") {
		t.Fatalf("stored token survived logout: %s", content)
	}

	// The revoked credential is refused afterwards: 401 maps to exit 5.
	code, stdout, _ = runCLI(t, "auth", "whoami", "--token", "stored-token", "--json")
	if code != ExitUnauthenticated {
		t.Fatalf("revoked token code = %d", code)
	}
	if envelope := decodeEnvelope(t, stdout); envelope.Error == nil || envelope.Error.HTTPStatus == nil || *envelope.Error.HTTPStatus != 401 {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestAuthLogoutLocalOnlySkipsServer(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	fake.valid["stored-token"] = true
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: stored-token\n", fake.server.URL))

	code, stdout, _ := runCLI(t, "auth", "logout", "--local-only")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if fake.logoutCall != 0 || !fake.tokenIsValid("stored-token") {
		t.Fatalf("local-only logout called the server or revoked state")
	}
	if !strings.Contains(stdout, "was not contacted") || !strings.Contains(stdout, "revoked from their own management surfaces") {
		t.Fatalf("stdout = %q", stdout)
	}
	if content := readConfigContent(t, configPath); strings.Contains(content, "stored-token") {
		t.Fatalf("stored token survived local-only logout: %s", content)
	}
}

func TestAuthLogoutWithoutCredentialsExitsFive(t *testing.T) {
	isolatedCLI(t)
	writeHomeConfig(t, "profiles:\n  prod:\n    server: https://gateway.example.com\n")
	code, _, stderr := runCLI(t, "auth", "logout")
	if code != ExitUnauthenticated || !strings.Contains(stderr, "no credentials") {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
}

func TestAuthChangePassword(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	fake.mustChange = true
	fake.valid["stored-token"] = true
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: stored-token\n", fake.server.URL))

	code, stdout, stderr := runCLIStdin(t, fake.password+"\nreplacement-password\n", "auth", "change-password", "--password-stdin", "--json")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	var data authChangePasswordResult
	if err := json.Unmarshal(decodeEnvelope(t, stdout).Data, &data); err != nil {
		t.Fatalf("data: %v", err)
	}
	if !data.ReLoginRequired || data.ProfileCleared != "prod" {
		t.Fatalf("data = %#v", data)
	}
	if fake.changeBody["currentPassword"] != "initial-password" || fake.changeBody["newPassword"] != "replacement-password" {
		t.Fatalf("server body = %#v", fake.changeBody)
	}
	if fake.password != "replacement-password" {
		t.Fatalf("server password = %q", fake.password)
	}
	if content := readConfigContent(t, configPath); strings.Contains(content, "stored-token") {
		t.Fatalf("stored token survived a password change: %s", content)
	}
	if strings.Contains(stdout, "replacement-password") || strings.Contains(stderr, "replacement-password") {
		t.Fatalf("new password leaked: %q / %q", stdout, stderr)
	}
}

func TestAuthChangePasswordRejectedKeepsToken(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	fake.valid["stored-token"] = true
	configPath := writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: stored-token\n", fake.server.URL))

	code, _, stderr := runCLIStdin(t, "wrong-current\nreplacement-password\n", "auth", "change-password", "--password-stdin")
	if code != ExitUnauthenticated {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	if content := readConfigContent(t, configPath); !strings.Contains(content, "stored-token") {
		t.Fatalf("stored token was cleared after a rejected change: %s", content)
	}
}

func TestAuthChangePasswordRequiresTwoBoundedLines(t *testing.T) {
	isolatedCLI(t)
	fake := newAuthFakeGateway(t)
	writeHomeConfig(t, fmt.Sprintf("profiles:\n  prod:\n    server: %s\n    token: stored-token\n", fake.server.URL))

	code, _, stderr := runCLI(t, "auth", "change-password")
	if code != ExitUsage || !strings.Contains(stderr, "--password-stdin") {
		t.Fatalf("missing flag: code = %d, stderr = %q", code, stderr)
	}
	code, _, stderr = runCLIStdin(t, "only-one-line\n", "auth", "change-password", "--password-stdin")
	if code != ExitUsage || !strings.Contains(stderr, "exactly two lines") {
		t.Fatalf("one line: code = %d, stderr = %q", code, stderr)
	}
	if fake.changeBody != nil {
		t.Fatalf("server received a change request from invalid input")
	}
}

func TestAuthGroupDispatch(t *testing.T) {
	isolatedCLI(t)
	tests := []struct {
		name     string
		args     []string
		code     int
		contains []string
	}{
		{"bare group", []string{"auth"}, ExitUsage, []string{"requires a subcommand", "login", "logout", "whoami", "change-password"}},
		{"group help", []string{"auth", "--help"}, ExitOK, []string{"subcommands:", "login", "change-password"}},
		{"subcommand help", []string{"auth", "login", "--help"}, ExitOK, []string{"agctl auth login", "--username", "--password-stdin"}},
		{"unknown subcommand", []string{"auth", "nope"}, ExitUsage, []string{"unknown command \"auth nope\"", "login"}},
		{"deep group help", []string{"config", "profile", "--help"}, ExitOK, []string{"add", "list", "use", "remove"}},
		{"deep bare group", []string{"config", "profile"}, ExitUsage, []string{"requires a subcommand", "add"}},
		{"config group help", []string{"config", "--help"}, ExitOK, []string{"profile"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, test.args...)
			if code != test.code {
				t.Fatalf("code = %d, want %d (stdout=%q stderr=%q)", code, test.code, stdout, stderr)
			}
			combined := stdout + stderr
			for _, want := range test.contains {
				if !strings.Contains(combined, want) {
					t.Fatalf("output missing %q:\n%s", want, combined)
				}
			}
		})
	}
}

func TestTopLevelUsageListsGroups(t *testing.T) {
	isolatedCLI(t)
	code, stdout, _ := runCLI(t, "--help")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	for _, want := range []string{"auth <subcommand>", "config <subcommand>", "version", "info", "commands"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("usage missing %q:\n%s", want, stdout)
		}
	}
}
