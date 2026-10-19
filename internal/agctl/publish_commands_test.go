package agctl

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func sha(content string) (string, string) {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:]), "sha-256=" + base64.StdEncoding.EncodeToString(sum[:])
}

// rawGateway is a fake Raw repository: HEAD answers stored digests, PUT
// verifies the Digest header and stores the body.
type rawGateway struct {
	server  *httptest.Server
	mu      sync.Mutex
	objects map[string]string
	puts    map[string]int
	fail    map[string]int // PUT answers 503 this many times
	badETag bool
	calls   int
}

func newRawGateway(t *testing.T) *rawGateway {
	t.Helper()
	isolatedCLI(t)
	gateway := &rawGateway{objects: map[string]string{}, puts: map[string]int{}, fail: map[string]int{}}
	gateway.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gateway.mu.Lock()
		defer gateway.mu.Unlock()
		gateway.calls++
		if r.URL.Path == "/api/v2/repositories/"+testRepoID {
			writeJSON(w, http.StatusOK, map[string]any{"id": testRepoID, "name": "raw-hosted", "format": "raw", "type": "hosted", "state": "active", "version": "1", "anonymousRead": false, "mavenStrictPublication": false})
			return
		}
		if r.URL.Path == "/api/v2/repositories/"+testRepoID+"/capabilities" {
			writeJSON(w, 200, map[string]any{"format": "raw", "type": "hosted", "operations": []string{"read", "publish"}})
			return
		}
		key := strings.TrimPrefix(r.URL.EscapedPath(), "/raw/raw-hosted/")
		switch r.Method {
		case http.MethodHead:
			content, ok := gateway.objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			// Reads answer the digest with a hyphen, as the Gateway does.
			digest, _ := sha(content)
			w.Header().Set("ETag", `"`+strings.Replace(digest, "sha256:", "sha256-", 1)+`"`)
			w.WriteHeader(http.StatusOK)
		case http.MethodPut:
			gateway.puts[key]++
			if gateway.fail[key] > 0 {
				gateway.fail[key]--
				writeProblem(w, http.StatusServiceUnavailable, "unavailable", "try again")
				return
			}
			digest, header := sha(string(body))
			if r.Header.Get("Digest") != header || r.ContentLength != int64(len(body)) {
				http.Error(w, "digest mismatch", http.StatusUnprocessableEntity)
				return
			}
			gateway.objects[key] = string(body)
			if gateway.badETag {
				digest = "sha256:0000"
			}
			w.Header().Set("ETag", `"`+digest+`"`)
			w.WriteHeader(http.StatusCreated)
		}
	}))
	t.Cleanup(gateway.server.Close)
	t.Setenv("AGCTL_SERVER", gateway.server.URL)
	t.Setenv("AGCTL_TOKEN", repoTestToken)
	return gateway
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func objectStatuses(t *testing.T, stdout string) map[string]string {
	t.Helper()
	var data struct {
		Items []objectResult `json:"items"`
	}
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	statuses := map[string]string{}
	for _, item := range data.Items {
		statuses[item.Path] = item.Status
	}
	return statuses
}

func TestPublishRawUploadsAndSkips(t *testing.T) {
	gateway := newRawGateway(t)
	dir := writeTree(t, map[string]string{"a.txt": "alpha", "nested/b with space.txt": "bravo"})
	gateway.objects["releases/1.0/a.txt"] = "alpha"
	code, stdout, stderr := runCLI(t, "publish", "raw", dir, "--repo", testRepoID, "--to", "releases/1.0", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	statuses := objectStatuses(t, stdout)
	if statuses["releases/1.0/a.txt"] != "skipped" || statuses["releases/1.0/nested/b with space.txt"] != "uploaded" {
		t.Fatalf("statuses = %v", statuses)
	}
	if gateway.objects["releases/1.0/nested/b%20with%20space.txt"] != "bravo" || gateway.puts["releases/1.0/a.txt"] != 0 {
		t.Fatalf("objects = %v, puts = %v", gateway.objects, gateway.puts)
	}
	code, stdout, _ = runCLI(t, "publish", "raw", dir, "--repo", testRepoID, "--to", "releases/1.0", "--force", "--json")
	if code != ExitOK || gateway.puts["releases/1.0/a.txt"] != 1 || !strings.Contains(stdout, `"uploaded"`) {
		t.Fatalf("--force uploads again: exit = %d, puts = %v", code, gateway.puts)
	}
}

func TestPublishRawPartialAndRetry(t *testing.T) {
	gateway := newRawGateway(t)
	dir := writeTree(t, map[string]string{"a.txt": "alpha", "b.txt": "bravo"})
	gateway.fail["b.txt"] = 1
	code, stdout, _ := runCLI(t, "publish", "raw", dir, "--repo", testRepoID, "--json")
	if code != ExitOK || gateway.puts["b.txt"] != 2 {
		t.Fatalf("a 503 is retried with the same bytes: exit = %d, puts = %v, stdout = %s", code, gateway.puts, stdout)
	}
	gateway.fail["a.txt"] = 10
	code, stdout, _ = runCLI(t, "publish", "raw", dir, "--repo", testRepoID, "--force", "--json")
	statuses := objectStatuses(t, stdout)
	if code != ExitPartial || statuses["a.txt"] != "unknown" || statuses["b.txt"] != "uploaded" {
		t.Fatalf("exit = %d, statuses = %v", code, statuses)
	}
	if hint := decodeEnvelope(t, stdout).Error.Hint; !strings.Contains(hint, "skipped by digest") {
		t.Fatalf("hint = %s", hint)
	}
	gateway.badETag = true
	gateway.fail["a.txt"] = 0
	code, stdout, _ = runCLI(t, "publish", "raw", filepath.Join(dir, "a.txt"), "--repo", testRepoID, "--to", "x/a.txt", "--json")
	if code != ExitPartial || objectStatuses(t, stdout)["x/a.txt"] != "unknown" {
		t.Fatalf("an ETag that names other bytes is not a confirmed upload: exit = %d, stdout = %s", code, stdout)
	}
}

func TestPublishRawValidatesBeforeRequest(t *testing.T) {
	gateway := newRawGateway(t)
	dir := writeTree(t, map[string]string{"a.txt": "alpha"})
	file := filepath.Join(dir, "a.txt")
	for _, args := range [][]string{
		{"publish", "raw", file},
		{"publish", "raw", file, "--repo", testRepoID, "--to", "../escape"},
		{"publish", "raw", file, "--repo", testRepoID, "--to", "/absolute"},
		{"publish", "raw", file, "--repo", testRepoID, "--parallel", "0"},
		{"publish", "raw", filepath.Join(dir, "missing"), "--repo", testRepoID},
		{"publish", "raw", t.TempDir(), "--repo", testRepoID},
	} {
		if code, _, _ := runCLI(t, args...); code != ExitUsage {
			t.Fatalf("%v exit = %d", args, code)
		}
	}
	if len(gateway.puts) != 0 {
		t.Fatalf("puts = %v", gateway.puts)
	}
	code, stdout, _ := runCLI(t, "publish", "raw", dir, "--repo", testRepoID, "--dry-run", "--json")
	if code != ExitOK || objectStatuses(t, stdout)["a.txt"] != "planned" || len(gateway.puts) != 0 {
		t.Fatalf("dry run exit = %d, puts = %v", code, gateway.puts)
	}
}

func TestPublishRawRefusesOtherRepositories(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase, 200, `{"id":"`+testRepoID+`","name":"npm-hosted","format":"npm","type":"hosted","state":"active","version":"1","anonymousRead":false,"mavenStrictPublication":false}`)
	file := filepath.Join(writeTree(t, map[string]string{"a.txt": "alpha"}), "a.txt")
	code, stdout, _ := runCLI(t, "publish", "raw", file, "--repo", testRepoID, "--json")
	if code != ExitUsage || !strings.Contains(stdout, "repo connect") || api.count("PUT") != 0 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

const sessionID = "00000000-0000-4000-e000-000000000001"

func sessionBody(state string) string {
	// The server encodes sessions with Go field names; decoding is case-insensitive.
	return `{"ID":"` + sessionID + `","RepositoryID":"` + testRepoID + `","Coordinate":"com.example:app:1.0.0","State":"` + state + `","ExpiresAt":"2026-10-15T09:00:00Z","Objects":[{"Name":"app-1.0.0.pom","Digest":"sha256:60c24224bbc16801180850bc99d8f099e79aaf26ddaac8fedaab553ecce43f20","Size":3},{"Name":"app-1.0.0.jar","Digest":"sha256:0163f1eea7894350060624d315234d40c508ab251ba121714e234503045faadd","Size":3}]}`
}

const artifactBody = `{"id":"00000000-0000-4000-a000-000000000009","coordinate":"com.example:app:1.0.0","digest":"sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae","createdAt":"2026-10-15T08:00:00Z","state":"visible"}`

func mavenFiles(t *testing.T) (string, string) {
	dir := writeTree(t, map[string]string{"app-1.0.0.pom": "pom", "app-1.0.0.jar": "jar"})
	return filepath.Join(dir, "app-1.0.0.pom"), filepath.Join(dir, "app-1.0.0.jar")
}

func allowMavenPublication(api *routeAPI) {
	api.on("GET", repoBase, 200, `{"id":"`+testRepoID+`","name":"maven-hosted","format":"maven","type":"hosted","state":"active","version":"1"}`)
	api.on("GET", repoBase+"/capabilities", 200, `{"format":"maven","type":"hosted","operations":["read","publish"]}`)
}

func TestPublishMavenOrchestrates(t *testing.T) {
	api := newRouteAPI(t, "")
	pom, jar := mavenFiles(t)
	expectUsage(t, api,
		[]string{"publish", "maven", "--repo", testRepoID, "--coordinate", "nope", "--pom", pom},
		[]string{"publish", "maven", "--repo", testRepoID, "--coordinate", "com.example:app:1.0.0"},
		[]string{"publish", "maven", "--repo", testRepoID, "--coordinate", "com.example:app:1.0.0", "--pom", pom, "--file", pom},
	)
	allowMavenPublication(api)
	api.on("POST", repoBase+"/publish-sessions", 201, sessionBody("open"))
	api.on("PUT", "/api/v2/publish-sessions/"+sessionID+"/objects/app-1.0.0.pom", 204, "")
	api.on("PUT", "/api/v2/publish-sessions/"+sessionID+"/objects/app-1.0.0.jar", 204, "")
	api.on("POST", "/api/v2/publish-sessions/"+sessionID+":commit", 200, artifactBody)
	code, stdout, stderr := runCLI(t, "publish", "maven", "--repo", testRepoID, "--coordinate", "com.example:app:1.0.0", "--pom", pom, "--file", jar, "--idempotency-key", "k-1", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"confirmedBy": "commit response"`) {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	requests := api.recorded()
	pomDigest, _ := sha("pom")
	if !strings.Contains(requests[2].Body, `"pomObject":"app-1.0.0.pom"`) || !strings.Contains(requests[2].Body, pomDigest) {
		t.Fatalf("create body = %s", requests[2].Body)
	}
	for _, request := range requests[3:5] {
		if request.Method != "PUT" || (request.Body != "pom" && request.Body != "jar") {
			t.Fatalf("upload = %+v", request)
		}
	}
}

func TestPublishMavenRefusesClosedSessionAndKeepsOpenOne(t *testing.T) {
	api := newRouteAPI(t, "")
	allowMavenPublication(api)
	pom, jar := mavenFiles(t)
	api.on("POST", repoBase+"/publish-sessions", 201, sessionBody("committed"))
	code, stdout, _ := runCLI(t, "publish", "maven", "--repo", testRepoID, "--coordinate", "com.example:app:1.0.0", "--pom", pom, "--file", jar, "--json")
	if code != ExitConflict || api.count("PUT") != 0 {
		t.Fatalf("a committed session is not reused: exit = %d, stdout = %s", code, stdout)
	}
	api.on("POST", repoBase+"/publish-sessions", 201, sessionBody("open"))
	api.on("PUT", "/api/v2/publish-sessions/"+sessionID+"/objects/app-1.0.0.pom", 204, "")
	api.on("PUT", "/api/v2/publish-sessions/"+sessionID+"/objects/app-1.0.0.jar", 422, `{"code":"digest_mismatch","message":"uploaded object digest does not match declaration"}`)
	code, stdout, _ = runCLI(t, "publish", "maven", "--repo", testRepoID, "--coordinate", "com.example:app:1.0.0", "--pom", pom, "--file", jar, "--idempotency-key", "k-2", "--json")
	envelope := decodeEnvelope(t, stdout)
	if code != ExitPartial || !strings.Contains(envelope.Error.Hint, "--idempotency-key k-2") || !strings.Contains(string(envelope.Data), sessionID) || api.count("POST") != 2 {
		t.Fatalf("a failed upload keeps the session open and says how to resume: exit = %d, stdout = %s", code, stdout)
	}
}

func TestPublishCommitIsReadBackWhenTheAnswerIsLost(t *testing.T) {
	api := newRouteAPI(t, "")
	var commits atomic.Int32
	api.onFunc("POST", "/api/v2/publish-sessions/"+sessionID+":commit", func(*http.Request) (int, string) {
		commits.Add(1)
		return 503, `{"code":"unavailable","message":"try again"}`
	})
	api.on("GET", "/api/v2/publish-sessions/"+sessionID, 200, sessionBody("committed"))
	code, stdout, _ := runCLI(t, "publish", "commit", sessionID, "--json")
	if code != ExitOK || commits.Load() != 1 || !strings.Contains(stdout, "session read-back") {
		t.Fatalf("a lost commit answer is confirmed by the session, never resent: exit = %d, commits = %d, stdout = %s", code, commits.Load(), stdout)
	}
	api.on("GET", "/api/v2/publish-sessions/"+sessionID, 200, sessionBody("open"))
	code, stdout, _ = runCLI(t, "publish", "commit", sessionID, "--json")
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" || commits.Load() != 2 {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "publish", "status", sessionID, "--json")
	if code != ExitOK || !strings.Contains(stdout, `"state": "open"`) || !strings.Contains(stdout, `"name": "app-1.0.0.jar"`) {
		t.Fatalf("status normalizes the session: exit = %d, stdout = %s", code, stdout)
	}
}

func TestLoginToSecretOutput(t *testing.T) {
	isolatedCLI(t)
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"token": issuedToken, "username": "alice", "role": "member", "mustChangePassword": false})
	}))
	t.Cleanup(login.Close)
	target := filepath.Join(t.TempDir(), "session")
	code, _, _ := runCLIStdin(t, userPassword+"\n", "auth", "login", "--server", login.URL, "--username", "alice", "--password-stdin", "--secret-output", target, "--profile", "x", "--json")
	if code != ExitUsage {
		t.Fatalf("--profile and --secret-output exclude each other: exit = %d", code)
	}
	code, stdout, stderr := runCLIStdin(t, userPassword+"\n", "auth", "login", "--server", login.URL, "--username", "alice", "--password-stdin", "--secret-output", target, "--verbose", "--json")
	noSecret(t, issuedToken, stdout, stderr)
	noSecret(t, userPassword, stdout, stderr)
	if code != ExitOK || !strings.Contains(stdout, `"delivered": true`) || strings.Contains(stdout, `"profile"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if data, _ := os.ReadFile(target); string(data) != issuedToken+"\n" {
		t.Fatalf("file = %q", data)
	}
	code, _, _ = runCLIStdin(t, userPassword+"\n", "auth", "login", "--server", login.URL, "--username", "alice", "--password-stdin", "--secret-output", target)
	if code != ExitUsage {
		t.Fatalf("an existing token file is never overwritten: exit = %d", code)
	}
}

func TestPublishCommandsAreRegisteredHonestly(t *testing.T) {
	for name, want := range map[string]struct{ retry bool }{
		"publish raw": {true}, "publish create": {true}, "publish upload": {true},
		"publish commit": {false}, "publish status": {true}, "publish maven": {true},
	} {
		cmd, ok := lookupCommand(name)
		if !ok || cmd.meta.Retry != want.retry {
			t.Fatalf("%s meta = %+v", name, cmd.meta)
		}
	}
}

func TestPublishCreationRejectsMismatchedSession(t *testing.T) {
	for name, body := range map[string]string{
		"repository":  strings.ReplaceAll(sessionBody("open"), testRepoID, "00000000-0000-4000-a000-000000000099"),
		"declaration": strings.ReplaceAll(sessionBody("open"), "sha256:60c24224bbc16801180850bc99d8f099e79aaf26ddaac8fedaab553ecce43f20", "sha256:wrong"),
		"state":       sessionBody("unknown"),
		"expiry":      strings.ReplaceAll(sessionBody("open"), "2026-10-15T09:00:00Z", "0001-01-01T00:00:00Z"),
	} {
		t.Run(name, func(t *testing.T) {
			api := newRouteAPI(t, "")
			pom, jar := mavenFiles(t)
			allowMavenPublication(api)
			api.on("POST", repoBase+"/publish-sessions", 201, body)
			code, stdout, _ := runCLI(t, "publish", "maven", "--repo", testRepoID, "--coordinate", "com.example:app:1.0.0", "--pom", pom, "--file", jar, "--idempotency-key", "recover-key", "--json")
			var recovery unknownOutcome
			envelope := decodeEnvelope(t, stdout)
			_ = json.Unmarshal(envelope.Data, &recovery)
			if code == ExitOK || envelope.Error == nil || envelope.Error.Code != "invalid_response" || recovery.Outcome != "unknown" || recovery.IdempotencyKey != "recover-key" || api.count("PUT") != 0 || api.count("POST") != 1 {
				t.Fatalf("unrelated session must not be used: exit=%d, stdout=%s", code, stdout)
			}
		})
	}
}

func TestPublishCommitRejectsUnprovenReadBack(t *testing.T) {
	for name, body := range map[string]string{
		"incomplete":    `{"state":"committed"}`,
		"other session": strings.ReplaceAll(sessionBody("committed"), sessionID, "00000000-0000-4000-e000-000000000099"),
	} {
		t.Run(name, func(t *testing.T) {
			api := newRouteAPI(t, "")
			api.on("POST", "/api/v2/publish-sessions/"+sessionID+":commit", 503, `{"code":"unavailable"}`)
			api.on("GET", "/api/v2/publish-sessions/"+sessionID, 200, body)
			code, stdout, _ := runCLI(t, "publish", "commit", sessionID, "--json")
			var recovery unknownOutcome
			_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &recovery)
			if code == ExitOK || recovery.Outcome != "unknown" || recovery.ID != sessionID || api.count("POST") != 1 {
				t.Fatalf("unproven commit must remain unknown: exit=%d, stdout=%s", code, stdout)
			}
		})
	}
}

func TestPublishRawRetryKeepsUnknownWhenSourceDisappears(t *testing.T) {
	api := newRouteAPI(t, "")
	file := filepath.Join(writeTree(t, map[string]string{"a.txt": "alpha"}), "a.txt")
	api.on("GET", repoBase, 200, `{"id":"`+testRepoID+`","name":"raw-hosted","format":"raw","type":"hosted","state":"active","version":"1"}`)
	api.on("GET", repoBase+"/capabilities", 200, `{"format":"raw","type":"hosted","operations":["read","publish"]}`)
	api.onFunc("PUT", "/raw/raw-hosted/releases/a.txt", func(*http.Request) (int, string) {
		if err := os.Remove(file); err != nil {
			t.Error(err)
		}
		return 503, `{"code":"unavailable"}`
	})
	code, stdout, _ := runCLI(t, "publish", "raw", file, "--repo", testRepoID, "--to", "releases/a.txt", "--force", "--json")
	if code != ExitPartial || objectStatuses(t, stdout)["releases/a.txt"] != "unknown" {
		t.Fatalf("an already-sent upload remains unknown: exit=%d, stdout=%s", code, stdout)
	}
	hint := decodeEnvelope(t, stdout).Error.Hint
	if !strings.Contains(hint, "original") || !strings.Contains(hint, "--to") {
		t.Fatalf("recovery must keep the original destination: %s", hint)
	}
}

func TestPublishUploadRecoveryKeepsFileArguments(t *testing.T) {
	api := newRouteAPI(t, "")
	pom, _ := mavenFiles(t)
	api.on("PUT", "/api/v2/publish-sessions/"+sessionID+"/objects/app-1.0.0.pom", 422, `{"code":"digest_mismatch"}`)
	code, stdout, _ := runCLI(t, "publish", "upload", sessionID, "--file", pom, "--json")
	hint := decodeEnvelope(t, stdout).Error.Hint
	if code != ExitPartial || !strings.Contains(hint, "original") || !strings.Contains(hint, "--file") || strings.Contains(hint, "skipped by digest") {
		t.Fatalf("recovery must keep upload files: exit=%d, hint=%s", code, hint)
	}
}
