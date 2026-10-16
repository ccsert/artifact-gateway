package agctl

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	accountID    = "00000000-0000-4000-c000-000000000001"
	credentialID = "00000000-0000-4000-c000-0000000000c1"
	userID       = "00000000-0000-4000-d000-000000000001"
	issuedToken  = "agc_SYNTHETIC-secret-marker-do-not-print"
	userPassword = "Synthetic-password-marker"
)

func accountJSON(id, name, version string) string {
	return fmt.Sprintf(`{"id":%q,"name":%q,"description":"","state":"active","createdAt":"2026-10-15T08:00:00Z","updatedAt":"2026-10-15T08:00:00Z","version":%q}`, id, name, version)
}

func userJSON(version string) string {
	return `{"id":"` + userID + `","name":"alice","displayName":"","email":"","description":"","role":"member","state":"active","localPasswordEnabled":true,"failedLoginAttempts":0,"mustChangePassword":false,"createdAt":"2026-10-15T08:00:00Z","version":"` + version + `"}`
}

// noSecret fails when a synthetic secret reached any output.
func noSecret(t *testing.T, secret string, outputs ...string) {
	t.Helper()
	for _, output := range outputs {
		if strings.Contains(output, secret) {
			t.Fatalf("a secret reached the output:\n%s", output)
		}
	}
}

func leftoverTemps(t *testing.T, dir string) []string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, ".agctl-secret-*"))
	return matches
}

func TestSecretDestinationRefusesUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", existing, link, filepath.Join(dir, "missing", "token")} {
		if _, err := prepareSecretDestination(path); err == nil {
			t.Fatalf("%q was accepted", path)
		} else if exitCodeFor(err) != ExitUsage {
			t.Fatalf("%q: %v is not a usage error", path, err)
		}
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep" || len(leftoverTemps(t, dir)) != 0 {
		t.Fatalf("a refused destination must leave everything untouched")
	}
}

func TestSecretDestinationDeliversPrivatelyWithoutOverwriting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "token")
	destination, err := prepareSecretDestination(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := destination.deliver(issuedToken); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	if data, _ := os.ReadFile(target); string(data) != issuedToken+"\n" || len(leftoverTemps(t, dir)) != 0 {
		t.Fatalf("content = %q, temps = %v", data, leftoverTemps(t, dir))
	}

	// A file that appears while the request runs is never overwritten.
	raced := filepath.Join(dir, "raced")
	destination, err = prepareSecretDestination(raced)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(raced, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = destination.deliver(issuedToken)
	if err == nil || strings.Contains(err.Error(), issuedToken) {
		t.Fatalf("err = %v", err)
	}
	if data, _ := os.ReadFile(raced); string(data) != "other" || len(leftoverTemps(t, dir)) != 0 {
		t.Fatalf("the other file was touched or a temporary file remained")
	}
}

func TestCredentialIssueWritesSecretOnlyToFile(t *testing.T) {
	api := newRouteAPI(t, "")
	dir := t.TempDir()
	target := filepath.Join(dir, "ci-token")
	expectUsage(t, api,
		[]string{"credential", "issue", "--service-account", accountID, "--name", "jenkins"},
		[]string{"credential", "issue", "--service-account", accountID, "--secret-output", target},
		[]string{"credential", "issue", "--name", "jenkins", "--secret-output", target},
		[]string{"credential", "issue", "--service-account", accountID, "--name", "jenkins", "--secret-output", target, "--expires-in", "9000h"},
	)
	path := "/api/v2/service-accounts/" + accountID + "/credentials"
	api.on("POST", path, 201, `{"id":"`+credentialID+`","serviceAccountId":"`+accountID+`","name":"jenkins","createdAt":"2026-10-15T08:00:00Z","expiresAt":"2027-01-13T08:00:00Z","token":"`+issuedToken+`"}`)
	code, stdout, stderr := runCLI(t, "credential", "issue", "--service-account", accountID, "--name", "jenkins", "--secret-output", target, "--expires-in", "720h", "--verbose", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	noSecret(t, issuedToken, stdout, stderr)
	var issued issuedCredential
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &issued)
	if issued.ID != credentialID || !issued.Secret.Delivered || issued.Secret.Path != target {
		t.Fatalf("issued = %+v", issued)
	}
	if data, _ := os.ReadFile(target); string(data) != issuedToken+"\n" {
		t.Fatalf("file = %q", data)
	}
	if body := api.recorded()[0].Body; !strings.Contains(body, `"name":"jenkins"`) || !strings.Contains(body, `"expiresAt"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestCredentialIssueFailuresNeverLeakOrResend(t *testing.T) {
	path := "/api/v2/service-accounts/" + accountID + "/credentials"
	t.Run("refused", func(t *testing.T) {
		api := newRouteAPI(t, "")
		dir := t.TempDir()
		api.on("POST", path, 409, `{"code":"service_account_disabled","message":"service account is disabled"}`)
		code, _, _ := runCLI(t, "credential", "issue", "--service-account", accountID, "--name", "x", "--secret-output", filepath.Join(dir, "t"))
		if code != ExitConflict || len(leftoverTemps(t, dir)) != 0 {
			t.Fatalf("exit = %d, temps = %v", code, leftoverTemps(t, dir))
		}
		if _, err := os.Stat(filepath.Join(dir, "t")); err == nil {
			t.Fatal("a refused issue created the file")
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		api := newRouteAPI(t, "")
		api.on("POST", path, 503, `{"code":"unavailable","message":"try again"}`)
		code, stdout, _ := runCLI(t, "credential", "issue", "--service-account", accountID, "--name", "x", "--secret-output", filepath.Join(t.TempDir(), "t"), "--json")
		var data unknownOutcome
		_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
		if code == ExitOK || data.Outcome != "unknown" || api.count("POST") != 1 {
			t.Fatalf("an issue is never resent: exit = %d, posts = %d, stdout = %s", code, api.count("POST"), stdout)
		}
	})
	t.Run("no token in the answer", func(t *testing.T) {
		api := newRouteAPI(t, "")
		dir := t.TempDir()
		api.on("POST", path, 201, `{"id":"`+credentialID+`","serviceAccountId":"`+accountID+`","name":"x","createdAt":"2026-10-15T08:00:00Z"}`)
		code, stdout, _ := runCLI(t, "credential", "issue", "--service-account", accountID, "--name", "x", "--secret-output", filepath.Join(dir, "t"), "--json")
		var data unknownOutcome
		_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
		if code == ExitOK || data.Outcome != "unknown" || len(leftoverTemps(t, dir)) != 0 {
			t.Fatalf("exit = %d, stdout = %s", code, stdout)
		}
	})
	t.Run("destination appears before delivery", func(t *testing.T) {
		api := newRouteAPI(t, "")
		dir := t.TempDir()
		target := filepath.Join(dir, "t")
		api.onFunc("POST", path, func(*http.Request) (int, string) {
			_ = os.WriteFile(target, []byte("someone else"), 0o600)
			return 201, `{"id":"` + credentialID + `","serviceAccountId":"` + accountID + `","name":"x","createdAt":"2026-10-15T08:00:00Z","token":"` + issuedToken + `"}`
		})
		code, stdout, stderr := runCLI(t, "credential", "issue", "--service-account", accountID, "--name", "x", "--secret-output", target, "--json")
		noSecret(t, issuedToken, stdout, stderr)
		envelope := decodeEnvelope(t, stdout)
		if code != ExitFailure || envelope.Error.Code != "secret_not_delivered" || !strings.Contains(envelope.Error.Hint, "agctl credential revoke "+credentialID) || !strings.Contains(string(envelope.Data), `"delivered": false`) {
			t.Fatalf("exit = %d, stdout = %s", code, stdout)
		}
		if data, _ := os.ReadFile(target); string(data) != "someone else" || api.count("POST") != 1 || len(leftoverTemps(t, dir)) != 0 {
			t.Fatalf("file = %q, posts = %d", data, api.count("POST"))
		}
	})
	t.Run("dry run creates nothing", func(t *testing.T) {
		api := newRouteAPI(t, "")
		dir := t.TempDir()
		code, stdout, _ := runCLI(t, "credential", "issue", "--service-account", accountID, "--name", "x", "--secret-output", filepath.Join(dir, "t"), "--dry-run", "--json")
		entries, _ := os.ReadDir(dir)
		if code != ExitOK || len(api.recorded()) != 0 || len(entries) != 0 || !strings.Contains(stdout, "writable directory") {
			t.Fatalf("exit = %d, entries = %v, stdout = %s", code, entries, stdout)
		}
	})
}

func TestAPIKeyCreateAndRevoke(t *testing.T) {
	api := newRouteAPI(t, "")
	dir := t.TempDir()
	expectUsage(t, api,
		[]string{"api-key", "create", "backup"},
		[]string{"api-key", "create", "backup", "--secret-output", filepath.Join(dir, "k"), "--role", "root"},
		[]string{"api-key", "revoke", credentialID},
	)
	api.on("POST", "/api/v2/api-keys", 201, `{"id":"`+credentialID+`","name":"backup","roles":["member"],"createdAt":"2026-10-15T08:00:00Z","token":"`+issuedToken+`"}`)
	code, stdout, stderr := runCLI(t, "api-key", "create", "backup", "--role", "member", "--secret-output", filepath.Join(dir, "k"), "--json")
	noSecret(t, issuedToken, stdout, stderr)
	if code != ExitOK || !strings.Contains(stdout, `"roles": [`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}

	var attempts atomic.Int32
	api.onFunc("DELETE", "/api/v2/api-keys/"+credentialID, func(*http.Request) (int, string) {
		if attempts.Add(1) == 1 {
			return 503, `{"code":"unavailable","message":"try again"}`
		}
		return 200, `{"id":"` + credentialID + `","name":"backup","roles":["member"],"createdAt":"2026-10-15T08:00:00Z","revokedAt":"2026-10-15T09:00:00Z"}`
	})
	code, stdout, _ = runCLI(t, "api-key", "revoke", credentialID, "--yes", "--json")
	if code != ExitOK || attempts.Load() != 2 || !strings.Contains(stdout, `"revokedAt"`) {
		t.Fatalf("a revoke is retried: exit = %d, attempts = %d, stdout = %s", code, attempts.Load(), stdout)
	}
	api.on("DELETE", "/api/v2/api-keys/"+credentialID, 200, `{"id":"`+credentialID+`","name":"backup","roles":[],"createdAt":"2026-10-15T08:00:00Z"}`)
	code, stdout, _ = runCLI(t, "api-key", "revoke", credentialID, "--yes", "--json")
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" {
		t.Fatalf("an answer without revokedAt is unknown: exit = %d, stdout = %s", code, stdout)
	}
}

func TestServiceAccountShowReadsTheWholeList(t *testing.T) {
	api := newRouteAPI(t, "")
	other := "00000000-0000-4000-c000-000000000002"
	api.onFunc("GET", "/api/v2/service-accounts", func(r *http.Request) (int, string) {
		if r.URL.Query().Get("pageToken") == "" {
			return 200, `{"items":[` + accountJSON(other, "a", "1") + `],"nextPageToken":"p2"}`
		}
		return 200, `{"items":[` + accountJSON(accountID, "ci-bot", "3") + `]}`
	})
	code, stdout, _ := runCLI(t, "service-account", "show", accountID)
	if code != ExitOK || !strings.Contains(stdout, "service-account:"+accountID) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	missing := "00000000-0000-4000-c000-00000000ffff"
	if code, _, _ := runCLI(t, "service-account", "show", missing); code != ExitNotFound {
		t.Fatalf("an account absent from the complete list exits 4: %d", code)
	}
	if code, _, _ := runCLI(t, "service-account", "show", missing, "--max-pages", "1"); code != ExitPartial {
		t.Fatalf("an unread rest of the list is not a 404: exit = %d", code)
	}
}

func TestServiceAccountCreateAndUpdate(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api, []string{"service-account", "update", accountID}, []string{"service-account", "update", accountID, "--state", "paused"})
	api.on("POST", "/api/v2/service-accounts", 503, `{"code":"unavailable","message":"try again"}`)
	code, stdout, _ := runCLI(t, "service-account", "create", "ci-bot", "--json")
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" || api.count("POST") != 1 {
		t.Fatalf("exit = %d, posts = %d, stdout = %s", code, api.count("POST"), stdout)
	}
	api.on("GET", "/api/v2/service-accounts", 200, `{"items":[`+accountJSON(accountID, "ci-bot", "3")+`]}`)
	api.on("PUT", "/api/v2/service-accounts/"+accountID, 200, strings.Replace(accountJSON(accountID, "ci-bot", "4"), `"active"`, `"disabled"`, 1))
	code, stdout, _ = runCLI(t, "service-account", "update", accountID, "--state", "disabled", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"state": "disabled"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	for _, request := range api.recorded() {
		if request.Method == "PUT" && (request.IfMatch != "3" || request.Body != `{"state":"disabled"}`) {
			t.Fatalf("put = %+v", request)
		}
	}
}

func TestUserCreateReadsPasswordSafely(t *testing.T) {
	api := newRouteAPI(t, "")
	file := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(file, []byte(userPassword+"\nignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	expectUsage(t, api,
		[]string{"user", "create", "alice", "--role", "member"},
		[]string{"user", "create", "alice", "--role", "member", "--password-stdin", "--password-file", file},
		[]string{"user", "create", "alice", "--role", "owner", "--password-file", file},
		[]string{"user", "create", "alice", "--role", "member", "--password-file", filepath.Join(t.TempDir(), "missing")},
	)
	if code, _, _ := runCLIStdin(t, "short\n", "user", "create", "alice", "--role", "member", "--password-stdin"); code != ExitUsage {
		t.Fatalf("short password exit = %d", code)
	}
	code, stdout, stderr := runCLIStdin(t, userPassword+"\n", "user", "create", "alice", "--role", "member", "--password-stdin", "--dry-run")
	noSecret(t, userPassword, stdout, stderr)
	if code != ExitOK || !strings.Contains(stdout, "<read from --password-stdin or --password-file; never shown>") || len(api.recorded()) != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	api.on("POST", "/api/v2/users", 201, userJSON("1"))
	code, stdout, stderr = runCLI(t, "user", "create", "alice", "--role", "member", "--password-file", file, "--email", "alice@example.com", "--verbose", "--json")
	noSecret(t, userPassword, stdout, stderr)
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if body := api.recorded()[0].Body; !strings.Contains(body, `"password":"`+userPassword+`"`) || strings.Contains(body, "ignored") {
		t.Fatalf("the first line of the file is the password: %s", body)
	}
	api.on("POST", "/api/v2/users", 400, `{"code":"invalid_request","message":"`+userPassword+` rejected"}`)
	_, stdout, stderr = runCLI(t, "user", "create", "alice", "--role", "member", "--password-file", file, "--json")
	noSecret(t, userPassword, stdout, stderr)
}

func TestPasswordFileMustBePrivateBeforeAnyRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions are not available")
	}
	for _, mode := range []os.FileMode{0o644, 0o640, 0o620} {
		t.Run(fmt.Sprintf("%o", mode), func(t *testing.T) {
			api := newRouteAPI(t, "")
			path := filepath.Join(t.TempDir(), "password")
			if err := os.WriteFile(path, []byte(userPassword+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"user", "create", "alice", "--role", "member", "--password-file", path, "--json"},
				{"user", "reset-password", userID, "--password-file", path, "--json"},
			} {
				code, stdout, stderr := runCLI(t, args...)
				noSecret(t, userPassword, stdout, stderr)
				if code != ExitUsage || len(api.recorded()) != 0 {
					t.Fatalf("a non-private password file must fail before requests: exit = %d, requests = %d, stdout = %s", code, len(api.recorded()), stdout)
				}
			}
		})
	}
}

func TestRevocationRequiresTheRequestedIdentity(t *testing.T) {
	other := "00000000-0000-4000-c000-000000000002"
	stamp := `"revokedAt":"2026-10-15T09:00:00Z"`
	for _, test := range []struct {
		name, path string
		args       []string
		bodies     []string
	}{
		{"credential", "/api/v2/service-accounts/" + accountID + "/credentials/" + credentialID,
			[]string{"credential", "revoke", credentialID, "--service-account", accountID, "--yes", "--json"},
			[]string{
				`{` + stamp + `}`,
				`{"id":"` + credentialID + `",` + stamp + `}`,
				`{"id":"` + other + `","serviceAccountId":"` + accountID + `",` + stamp + `}`,
				`{"id":"` + credentialID + `","serviceAccountId":"` + other + `",` + stamp + `}`,
			}},
		{"api-key", "/api/v2/api-keys/" + credentialID,
			[]string{"api-key", "revoke", credentialID, "--yes", "--json"},
			[]string{`{` + stamp + `}`, `{"id":"` + other + `",` + stamp + `}`}},
		{"session", "/api/v2/users/" + userID + "/sessions/" + credentialID,
			[]string{"user", "sessions", "revoke", userID, "--session", credentialID, "--yes", "--json"},
			[]string{
				`{` + stamp + `}`,
				`{"id":"` + credentialID + `",` + stamp + `}`,
				`{"id":"` + other + `","userId":"` + userID + `",` + stamp + `}`,
				`{"id":"` + credentialID + `","userId":"` + other + `",` + stamp + `}`,
			}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for index, body := range test.bodies {
				t.Run(fmt.Sprint(index), func(t *testing.T) {
					api := newRouteAPI(t, "")
					api.on("DELETE", test.path, 200, body)
					code, stdout, _ := runCLI(t, test.args...)
					envelope := decodeEnvelope(t, stdout)
					var data unknownOutcome
					_ = json.Unmarshal(envelope.Data, &data)
					if code == ExitOK || envelope.Error == nil || envelope.Error.Code != "invalid_response" || data.Outcome != "unknown" || api.count("DELETE") != 1 {
						t.Fatalf("an unconfirmed revocation must not succeed or repeat: exit = %d, stdout = %s", code, stdout)
					}
				})
			}
		})
	}
}

func TestServiceAccountShowRejectsIncompleteList(t *testing.T) {
	for _, body := range []string{"", "null", `{}`, `{"items":null}`, `{"items":[{}]}`} {
		t.Run(body, func(t *testing.T) {
			api := newRouteAPI(t, "")
			api.on("GET", "/api/v2/service-accounts", 200, body)
			code, stdout, _ := runCLI(t, "service-account", "show", accountID, "--json")
			envelope := decodeEnvelope(t, stdout)
			if code == ExitOK || code == ExitNotFound || envelope.Error == nil || envelope.Error.Code != "invalid_response" {
				t.Fatalf("an incomplete list cannot prove not found: exit = %d, stdout = %s", code, stdout)
			}
		})
	}
}

func TestUserIdentitySubjectUsesCharacterLimit(t *testing.T) {
	api := newRouteAPI(t, "")
	path := "/api/v2/users/" + userID + "/identities"
	api.on("POST", path, 201, `{"id":"`+credentialID+`","userId":"`+userID+`","kind":"oidc","issuer":"https://id.example.com","subject":"`+strings.Repeat("用", 512)+`"}`)
	code, stdout, _ := runCLI(t, "user", "identities", "link", userID, "--issuer", "https://id.example.com", "--subject", strings.Repeat("用", 512), "--json")
	if code != ExitOK || api.count("POST") != 1 || !strings.Contains(api.recorded()[0].Body, strings.Repeat("用", 512)) {
		t.Fatalf("512 Unicode characters must be accepted: exit = %d, stdout = %s", code, stdout)
	}
	code, _, _ = runCLI(t, "user", "identities", "link", userID, "--issuer", "https://id.example.com", "--subject", strings.Repeat("用", 513))
	if code != ExitUsage || api.count("POST") != 1 {
		t.Fatalf("513 characters must fail before another request: exit = %d", code)
	}
}

func TestUserListOffsetWindow(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", "/api/v2/users", 200, `{"items":[`+userJSON("1")+`],"total":3,"offset":1,"limit":1}`)
	code, stdout, _ := runCLI(t, "user", "list", "--role", "member", "--limit", "1", "--offset", "1", "--json")
	meta := string(decodeEnvelope(t, stdout).Meta)
	if code != ExitOK || !strings.Contains(meta, `"total": 3`) || !strings.Contains(meta, `"complete": false`) {
		t.Fatalf("exit = %d, meta = %s", code, meta)
	}
	if query := api.recorded()[0].Query; query != "limit=1&offset=1&role=member" {
		t.Fatalf("query = %s", query)
	}
}

func TestUserConditionalWrites(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"user", "update", userID},
		[]string{"user", "update", userID, "--state", "gone"},
		[]string{"user", "update", userID, "--email", "not an email"},
		[]string{"user", "delete", userID},
		[]string{"user", "sessions", "revoke", userID, "--yes"},
		[]string{"user", "sessions", "revoke", userID, "--all", "--session", "s", "--yes"},
		[]string{"user", "sessions", "revoke", userID, "--all"},
		[]string{"user", "identities", "link", userID, "--issuer", "not-a-url", "--subject", "a"},
		[]string{"user", "identities", "unlink", userID, "--identity", "i"},
	)
	api.on("GET", "/api/v2/users/"+userID, 200, userJSON("5"))
	api.on("PATCH", "/api/v2/users/"+userID, 409, `{"code":"last_admin","message":"the last active administrator cannot be disabled or demoted"}`)
	if code, _, _ := runCLI(t, "user", "update", userID, "--role", "none"); code != ExitConflict {
		t.Fatalf("last admin exit = %d", code)
	}
	api.on("POST", "/api/v2/users/"+userID+"/password", 412, `{"code":"version_conflict","message":"If-Match does not match current version"}`)
	code, stdout, stderr := runCLIStdin(t, userPassword+"\n", "user", "reset-password", userID, "--password-stdin", "--json")
	noSecret(t, userPassword, stdout, stderr)
	if code != ExitConflict || api.count("POST") != 1 {
		t.Fatalf("exit = %d, posts = %d", code, api.count("POST"))
	}
	api.on("POST", "/api/v2/users/"+userID+"/sessions:revoke", 200, userJSON("6"))
	code, _, _ = runCLI(t, "user", "sessions", "revoke", userID, "--all", "--yes")
	if code != ExitOK {
		t.Fatalf("revoke all exit = %d", code)
	}
	for _, request := range api.recorded() {
		if request.Method != "GET" && request.IfMatch != "5" {
			t.Fatalf("every conditional write uses the version read: %+v", request)
		}
	}
	var attempts atomic.Int32
	api.onFunc("DELETE", "/api/v2/users/"+userID+"/sessions/"+credentialID, func(*http.Request) (int, string) {
		if attempts.Add(1) == 1 {
			return 502, `{"code":"bad_gateway","message":"upstream"}`
		}
		return 200, `{"id":"` + credentialID + `","userId":"` + userID + `","kind":"local_session","ipAddress":"","userAgent":"","createdAt":"2026-10-15T08:00:00Z","expiresAt":"2026-10-15T20:00:00Z","revokedAt":"2026-10-15T09:00:00Z","current":false}`
	})
	code, _, _ = runCLI(t, "user", "sessions", "revoke", userID, "--session", credentialID, "--yes")
	if code != ExitOK || attempts.Load() != 2 {
		t.Fatalf("ending one session is retried: exit = %d, attempts = %d", code, attempts.Load())
	}
	api.on("DELETE", "/api/v2/users/"+userID, 503, `{"code":"unavailable","message":"try again"}`)
	code, stdout, _ = runCLI(t, "user", "delete", userID, "--yes", "--json")
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" || api.count("DELETE") != 3 {
		t.Fatalf("a delete is not retried: exit = %d, deletes = %d (2 session attempts + 1)", code, api.count("DELETE"))
	}
}

func TestIdentityCommandsAreRegisteredHonestly(t *testing.T) {
	want := map[string]struct {
		mutating, retry bool
	}{
		"service-account list": {false, true}, "service-account show": {false, true},
		"service-account create": {true, false}, "service-account update": {true, false},
		"credential list": {false, true}, "credential issue": {true, false}, "credential revoke": {true, true},
		"api-key list": {false, true}, "api-key create": {true, false}, "api-key revoke": {true, true},
		"user list": {false, true}, "user show": {false, true}, "user create": {true, false}, "user update": {true, false},
		"user delete": {true, false}, "user reset-password": {true, false}, "user sessions list": {false, true},
		"user sessions revoke": {true, true}, "user identities list": {false, true},
		"user identities link": {true, false}, "user identities unlink": {true, false},
	}
	for name, expected := range want {
		cmd, ok := lookupCommand(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if cmd.meta.Mutating != expected.mutating || cmd.meta.Retry != expected.retry || len(cmd.meta.Endpoints) == 0 {
			t.Fatalf("%s meta = %+v", name, cmd.meta)
		}
		if expected.mutating && cmd.meta.DryRun != "client-plan" {
			t.Fatalf("%s must plan: %+v", name, cmd.meta)
		}
	}
}
