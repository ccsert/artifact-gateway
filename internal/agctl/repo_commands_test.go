package agctl

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files under testdata/golden")

const repoTestToken = "repo-test-token"

type fakeRepository struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Format        string   `json:"format"`
	Type          string   `json:"type"`
	State         string   `json:"state"`
	Version       string   `json:"version"`
	AnonymousRead bool     `json:"anonymousRead"`
	MavenStrict   bool     `json:"mavenStrictPublication"`
	Endpoint      string   `json:"endpoint,omitempty"`
	AllowedHosts  []string `json:"allowedHosts,omitempty"`
}

type recordedRequest struct {
	Method         string
	Path           string
	Query          string
	IfMatch        string
	IdempotencyKey string
	Body           string
}

// fakeRepositoryAPI is an in-memory stand-in for the repository endpoints with
// the server semantics the commands depend on: cursor pages, idempotent
// create replay, If-Match updates, and asynchronous deletion.
type fakeRepositoryAPI struct {
	t      *testing.T
	server *httptest.Server

	mu           sync.Mutex
	repos        []*fakeRepository
	requests     []recordedRequest
	idempotency  map[string]string
	failCreates  int // answer the next N creates with 503 after applying them
	failPages    map[int]bool
	deleteAfter  int // GETs of a deleting repository before it turns deleted; <0 never
	deletingGets map[string]int
}

func newFakeRepositoryAPI(t *testing.T) *fakeRepositoryAPI {
	t.Helper()
	isolatedCLI(t)
	api := &fakeRepositoryAPI{t: t, idempotency: map[string]string{}, failPages: map[int]bool{}, deletingGets: map[string]int{}}
	api.server = httptest.NewServer(http.HandlerFunc(api.handle))
	t.Cleanup(api.server.Close)
	t.Setenv("AGCTL_SERVER", api.server.URL)
	t.Setenv("AGCTL_TOKEN", repoTestToken)
	return api
}

func (api *fakeRepositoryAPI) add(name, format, kind string) *fakeRepository {
	api.mu.Lock()
	defer api.mu.Unlock()
	repo := &fakeRepository{
		ID:      fmt.Sprintf("00000000-0000-4000-8000-%012d", len(api.repos)+1),
		Name:    name,
		Format:  format,
		Type:    kind,
		State:   "active",
		Version: "1",
	}
	api.repos = append(api.repos, repo)
	return repo
}

func (api *fakeRepositoryAPI) writes() []recordedRequest {
	api.mu.Lock()
	defer api.mu.Unlock()
	var writes []recordedRequest
	for _, request := range api.requests {
		if request.Method != http.MethodGet {
			writes = append(writes, request)
		}
	}
	return writes
}

func (api *fakeRepositoryAPI) find(id string) *fakeRepository {
	for _, repo := range api.repos {
		if repo.ID == id {
			return repo
		}
	}
	return nil
}

func (api *fakeRepositoryAPI) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	api.mu.Lock()
	defer api.mu.Unlock()
	api.requests = append(api.requests, recordedRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
		IfMatch: r.Header.Get("If-Match"), IdempotencyKey: r.Header.Get("Idempotency-Key"), Body: string(body),
	})
	if r.Header.Get("Authorization") != "Bearer "+repoTestToken {
		writeProblem(w, http.StatusUnauthorized, "access_denied", "authentication required")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/repositories")
	if path == r.URL.Path {
		writeProblem(w, http.StatusNotFound, "not_found", "no route")
		return
	}
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			api.list(w, r)
		case http.MethodPost:
			api.create(w, r, body)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	repo := api.find(strings.TrimPrefix(path, "/"))
	if repo == nil || (r.Method == http.MethodDelete && repo.State == "deleted") {
		writeProblem(w, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if repo.State == "deleting" && api.deleteAfter >= 0 {
			api.deletingGets[repo.ID]++
			if api.deletingGets[repo.ID] > api.deleteAfter {
				repo.State = "deleted"
				repo.Version = bump(repo.Version)
			}
		}
		writeJSON(w, http.StatusOK, repo)
	case http.MethodPatch:
		if r.Header.Get("If-Match") != repo.Version {
			writeProblem(w, http.StatusPreconditionFailed, "version_conflict", "If-Match does not match current version")
			return
		}
		var change struct {
			AnonymousRead *bool     `json:"anonymousRead"`
			Endpoint      *string   `json:"endpoint"`
			AllowedHosts  *[]string `json:"allowedHosts"`
		}
		_ = json.Unmarshal(body, &change)
		if change.AnonymousRead != nil {
			repo.AnonymousRead = *change.AnonymousRead
		}
		if change.Endpoint != nil {
			repo.Endpoint = *change.Endpoint
		}
		if change.AllowedHosts != nil {
			repo.AllowedHosts = *change.AllowedHosts
		}
		repo.Version = bump(repo.Version)
		w.Header().Set("ETag", repo.Version)
		writeJSON(w, http.StatusOK, repo)
	case http.MethodDelete:
		if repo.State == "active" {
			repo.State = "deleting"
			repo.Version = bump(repo.Version)
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"id": repo.ID, "state": "pending"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api *fakeRepositoryAPI) list(w http.ResponseWriter, r *http.Request) {
	size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	start := 0
	if token := r.URL.Query().Get("pageToken"); token != "" {
		start, _ = strconv.Atoi(strings.TrimPrefix(token, "cursor-"))
	}
	if api.failPages[start] {
		writeProblem(w, http.StatusInternalServerError, "internal_error", "list repositories failed")
		return
	}
	end := start + size
	if end > len(api.repos) {
		end = len(api.repos)
	}
	page := map[string]any{"items": api.repos[start:end]}
	if end < len(api.repos) {
		page["nextPageToken"] = fmt.Sprintf("cursor-%d", end)
	}
	writeJSON(w, http.StatusOK, page)
}

func (api *fakeRepositoryAPI) create(w http.ResponseWriter, r *http.Request, body []byte) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeProblem(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key is required")
		return
	}
	var request struct {
		Name   string `json:"name"`
		Format string `json:"format"`
		Type   string `json:"type"`
	}
	_ = json.Unmarshal(body, &request)
	var repo *fakeRepository
	if previous, ok := api.idempotency[key]; ok {
		if previous != string(body) {
			writeProblem(w, http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used with a different request")
			return
		}
		for _, candidate := range api.repos {
			if candidate.Name == request.Name {
				repo = candidate
			}
		}
	} else {
		for _, candidate := range api.repos {
			if candidate.Name == request.Name {
				writeProblem(w, http.StatusConflict, "version_conflict", "repository name already exists")
				return
			}
		}
		api.idempotency[key] = string(body)
		api.mu.Unlock()
		repo = api.add(request.Name, request.Format, request.Type)
		api.mu.Lock()
	}
	if api.failCreates > 0 {
		api.failCreates--
		writeProblem(w, http.StatusServiceUnavailable, "unavailable", "try again")
		return
	}
	writeJSON(w, http.StatusCreated, repo)
}

func bump(version string) string {
	value, _ := strconv.Atoi(version)
	return strconv.Itoa(value + 1)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// assertGolden compares a JSON envelope with testdata/golden/NAME.json after
// replacing the fake server URL; `go test -run Repo -update` rewrites it.
func assertGolden(t *testing.T, name, output string, replacements ...string) {
	t.Helper()
	for index := 0; index+1 < len(replacements); index += 2 {
		output = strings.ReplaceAll(output, replacements[index], replacements[index+1])
	}
	path := filepath.Join("testdata", "golden", name+".json")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run go test -update to create it): %v", path, err)
	}
	if string(want) != output {
		t.Fatalf("%s differs from the golden file:\n--- got\n%s\n--- want\n%s", name, output, want)
	}
}

func TestRepoListReadsOnePageByDefault(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	api.add("npm-hosted", "npm", "hosted")
	api.add("raw-proxy", "raw", "proxy")
	api.add("maven-hosted", "maven", "hosted")

	code, stdout, stderr := runCLI(t, "repo", "list", "--page-size", "2", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	assertGolden(t, "repo_list_first_page", stdout)
	if len(api.requests) != 1 || api.requests[0].Query != "pageSize=2" {
		t.Fatalf("requests = %+v", api.requests)
	}

	code, stdout, _ = runCLI(t, "repo", "list", "--page-size", "2", "--cursor", "cursor-2")
	if code != ExitOK || !strings.Contains(stdout, "maven-hosted") || strings.Contains(stdout, "npm-hosted") {
		t.Fatalf("second page exit = %d, stdout = %s", code, stdout)
	}
}

func TestRepoListAllFollowsCursors(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	for index := 0; index < 5; index++ {
		api.add(fmt.Sprintf("repo-%d", index), "raw", "hosted")
	}
	code, stdout, stderr := runCLI(t, "repo", "list", "--all", "--page-size", "2", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	envelope := decodeEnvelope(t, stdout)
	var data struct {
		Items      []fakeRepository `json:"items"`
		NextCursor *string          `json:"nextCursor"`
	}
	_ = json.Unmarshal(envelope.Data, &data)
	if len(data.Items) != 5 || data.NextCursor != nil || !strings.Contains(string(envelope.Meta), `"pages": 3`) || !strings.Contains(string(envelope.Meta), `"complete": true`) {
		t.Fatalf("data = %+v, meta = %s", data, envelope.Meta)
	}
}

func TestRepoListAllBudgetIsPartial(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	for index := 0; index < 5; index++ {
		api.add(fmt.Sprintf("repo-%d", index), "raw", "hosted")
	}
	code, stdout, _ := runCLI(t, "repo", "list", "--all", "--page-size", "2", "--max-pages", "2", "--json")
	if code != ExitPartial {
		t.Fatalf("exit = %d, want %d", code, ExitPartial)
	}
	assertGolden(t, "repo_list_budget_partial", stdout)
}

func TestRepoListFailureAfterFirstPageIsPartial(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	for index := 0; index < 3; index++ {
		api.add(fmt.Sprintf("repo-%d", index), "raw", "hosted")
	}
	api.failPages[2] = true
	code, stdout, stderr := runCLI(t, "repo", "list", "--all", "--page-size", "2")
	if code != ExitPartial || !strings.Contains(stdout, "repo-1") || !strings.Contains(stderr, "listing stopped after 1 pages") {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}

	// A first page that fails is an ordinary failure, not an empty result.
	api.failPages[0] = true
	code, stdout, _ = runCLI(t, "repo", "list", "--json")
	if code != ExitFailure || decodeEnvelope(t, stdout).Error.HTTPStatus == nil {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestRepoListRejectsInvalidInputBeforeRequest(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	for _, args := range [][]string{
		{"repo", "list", "--page-size", "0"},
		{"repo", "list", "--page-size", "201"},
		{"repo", "list", "--all", "--cursor", "cursor-2"},
		{"repo", "list", "--all", "--max-pages", "0"},
	} {
		if code, _, _ := runCLI(t, args...); code != ExitUsage {
			t.Fatalf("%v exit = %d, want %d", args, code, ExitUsage)
		}
	}
	if len(api.requests) != 0 {
		t.Fatalf("requests = %+v", api.requests)
	}
}

func TestRepoListEmptyIsSuccess(t *testing.T) {
	newFakeRepositoryAPI(t)
	code, stdout, _ := runCLI(t, "repo", "list")
	if code != ExitOK || !strings.Contains(stdout, "no repositories visible") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestRepoShowMapsNotFound(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	repo := api.add("npm-hosted", "npm", "hosted")
	code, stdout, _ := runCLI(t, "repo", "show", repo.ID)
	if code != ExitOK || !strings.Contains(stdout, "npm-hosted") || !strings.Contains(stdout, "version:") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if code, _, _ := runCLI(t, "repo", "show", "00000000-0000-4000-8000-999999999999"); code != ExitNotFound {
		t.Fatalf("missing repository exit = %d, want %d", code, ExitNotFound)
	}
	if code, _, _ := runCLI(t, "repo", "show"); code != ExitUsage {
		t.Fatalf("missing ID exit = %d, want %d", code, ExitUsage)
	}
}

func TestRepoCreateSendsIdempotencyKeyAndReusesItOnRetry(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	api.failCreates = 1
	code, stdout, stderr := runCLI(t, "repo", "create", "npm-hosted", "--format", "npm", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	writes := api.writes()
	if len(writes) != 2 || writes[0].IdempotencyKey == "" || writes[0].IdempotencyKey != writes[1].IdempotencyKey || writes[0].Body != writes[1].Body {
		t.Fatalf("a retried create must resend the same key and body: %+v", writes)
	}
	if len(api.repos) != 1 {
		t.Fatalf("repositories = %d, want exactly one", len(api.repos))
	}
}

func TestRepoCreateUnknownOutcomeReportsKey(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	api.failCreates = 10
	code, stdout, _ := runCLI(t, "repo", "create", "npm-hosted", "--format", "npm", "--idempotency-key", "agctl-fixed-key", "--json")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	assertGolden(t, "repo_create_unknown_outcome", stdout)

	// Re-running with the reported key replays the original creation.
	api.failCreates = 0
	code, stdout, _ = runCLI(t, "repo", "create", "npm-hosted", "--format", "npm", "--idempotency-key", "agctl-fixed-key")
	if code != ExitOK || len(api.repos) != 1 {
		t.Fatalf("replay exit = %d, repositories = %d, stdout = %s", code, len(api.repos), stdout)
	}
	// A different request under the same key is refused, never applied.
	code, _, _ = runCLI(t, "repo", "create", "other", "--format", "npm", "--idempotency-key", "agctl-fixed-key")
	if code != ExitConflict || len(api.repos) != 1 {
		t.Fatalf("conflict exit = %d, repositories = %d", code, len(api.repos))
	}
}

func TestRepoCreateValidatesBeforeRequest(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	for _, args := range [][]string{
		{"repo", "create", "Bad_Name", "--format", "npm"},
		{"repo", "create", "npm-hosted"},
		{"repo", "create", "npm-hosted", "--format", "rpm"},
		{"repo", "create", "npm-hosted", "--format", "npm", "--type", "group"},
		{"repo", "create", "npm-proxy", "--format", "npm", "--type", "proxy"},
		{"repo", "create", "npm-hosted", "--format", "npm", "--endpoint", "https://registry.npmjs.org"},
		{"repo", "create", "npm-hosted", "--format", "npm", "--maven-strict-publication"},
		{"repo", "create", "npm-hosted", "--format", "npm", "--idempotency-key", " "},
	} {
		if code, _, _ := runCLI(t, args...); code != ExitUsage {
			t.Fatalf("%v exit = %d, want %d", args, code, ExitUsage)
		}
	}
	if len(api.requests) != 0 {
		t.Fatalf("requests = %+v", api.requests)
	}
}

func TestRepoCreateDryRunSubmitsNothing(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	code, stdout, _ := runCLI(t, "repo", "create", "npm-proxy", "--format", "npm", "--type", "proxy",
		"--endpoint", "https://registry.npmjs.org", "--allowed-host", "registry.npmjs.org",
		"--idempotency-key", "agctl-fixed-key", "--dry-run", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "repo_create_dry_run", stdout)
	if len(api.requests) != 0 {
		t.Fatalf("a client-plan create must not send requests: %+v", api.requests)
	}
}

func TestRepoUpdateUsesReadVersionAndNeverOverwritesOnConflict(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	repo := api.add("npm-hosted", "npm", "hosted")

	code, stdout, stderr := runCLI(t, "repo", "update", repo.ID, "--anonymous-read=true", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	writes := api.writes()
	if len(writes) != 1 || writes[0].IfMatch != "1" || writes[0].Body != `{"anonymousRead":true}` {
		t.Fatalf("writes = %+v", writes)
	}

	// A stale --if-match fails with 412 and leaves the repository unchanged.
	code, stdout, _ = runCLI(t, "repo", "update", repo.ID, "--anonymous-read=false", "--if-match", "1", "--json")
	envelope := decodeEnvelope(t, stdout)
	if code != ExitConflict || envelope.Error.Code != "version_conflict" || !strings.Contains(envelope.Error.Hint, "was not updated") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if !repo.AnonymousRead || repo.Version != "2" {
		t.Fatalf("a 412 must not overwrite: %+v", repo)
	}
	if writes := api.writes(); len(writes) != 2 {
		t.Fatalf("a conditional update must not be retried: %+v", writes)
	}
}

func TestRepoUpdateRequiresAChange(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	repo := api.add("npm-hosted", "npm", "hosted")
	if code, _, _ := runCLI(t, "repo", "update", repo.ID); code != ExitUsage {
		t.Fatalf("exit = %d, want %d", code, ExitUsage)
	}
	if code, _, _ := runCLI(t, "repo", "update", repo.ID, "--anonymous-read", "--if-match", ""); code != ExitUsage {
		t.Fatalf("empty --if-match exit = %d, want %d", code, ExitUsage)
	}
	if len(api.requests) != 0 {
		t.Fatalf("requests = %+v", api.requests)
	}
}

func TestRepoUpdateDryRunReadsButDoesNotWrite(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	repo := api.add("raw-proxy", "raw", "proxy")
	code, stdout, _ := runCLI(t, "repo", "update", repo.ID, "--allowed-host", "a.example", "--allowed-host", "b.example", "--dry-run", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "repo_update_dry_run", stdout, api.server.URL, "http://gateway.test")
	if writes := api.writes(); len(writes) != 0 {
		t.Fatalf("writes = %+v", writes)
	}
}

func TestRepoDeleteRequiresConfirmation(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	repo := api.add("npm-hosted", "npm", "hosted")
	code, stdout, _ := runCLI(t, "repo", "delete", repo.ID, "--json")
	if code != ExitUsage || !strings.Contains(stdout, "--yes") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if len(api.requests) != 0 || repo.State != "active" {
		t.Fatalf("requests = %+v, state = %s", api.requests, repo.State)
	}
}

func TestRepoDeleteDryRunReportsCurrentState(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	repo := api.add("npm-hosted", "npm", "hosted")
	code, stdout, _ := runCLI(t, "repo", "delete", repo.ID, "--dry-run", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "repo_delete_dry_run", stdout)
	if len(api.writes()) != 0 || repo.State != "active" {
		t.Fatalf("writes = %+v, state = %s", api.writes(), repo.State)
	}
}

func TestRepoDeleteAcceptedIsNotCompletion(t *testing.T) {
	api := newFakeRepositoryAPI(t)
	repo := api.add("npm-hosted", "npm", "hosted")
	code, stdout, _ := runCLI(t, "repo", "delete", repo.ID, "--yes", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "repo_delete_accepted", stdout)
}

func TestRepoDeleteWaitReachesDeleted(t *testing.T) {
	previous := repoWaitInterval
	repoWaitInterval = 10 * time.Millisecond
	t.Cleanup(func() { repoWaitInterval = previous })
	api := newFakeRepositoryAPI(t)
	api.deleteAfter = 2
	repo := api.add("npm-hosted", "npm", "hosted")

	code, stdout, stderr := runCLI(t, "repo", "delete", repo.ID, "--yes", "--wait", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	assertGolden(t, "repo_delete_wait_deleted", stdout)
	// The deleted repository stays readable; it is not assumed to be gone.
	if code, _, _ := runCLI(t, "repo", "show", repo.ID); code != ExitOK {
		t.Fatalf("show after delete exit = %d", code)
	}
}

func TestRepoDeleteWaitTimeoutKeepsServerWorking(t *testing.T) {
	previous := repoWaitInterval
	repoWaitInterval = 10 * time.Millisecond
	t.Cleanup(func() { repoWaitInterval = previous })
	api := newFakeRepositoryAPI(t)
	api.deleteAfter = -1
	repo := api.add("npm-hosted", "npm", "hosted")

	code, stdout, _ := runCLI(t, "repo", "delete", repo.ID, "--yes", "--wait", "--timeout", "200ms", "--json")
	if code != ExitConnection {
		t.Fatalf("exit = %d, want %d: %s", code, ExitConnection, stdout)
	}
	assertGolden(t, "repo_delete_wait_timeout", stdout)
	deletes := 0
	for _, write := range api.writes() {
		if write.Method == http.MethodDelete {
			deletes++
		}
	}
	if deletes != 1 || repo.State != "deleting" {
		t.Fatalf("a wait timeout must not resend or cancel: deletes = %d, state = %s", deletes, repo.State)
	}
}

func TestRepoCommandsAreRegisteredHonestly(t *testing.T) {
	want := map[string]struct {
		mutating bool
		dryRun   string
		wait     bool
	}{
		"repo list":   {false, "unsupported", false},
		"repo show":   {false, "unsupported", false},
		"repo create": {true, "client-plan", false},
		"repo update": {true, "client-plan", false},
		"repo delete": {true, "client-plan", true},
	}
	for name, expected := range want {
		cmd, ok := lookupCommand(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if cmd.meta.Mutating != expected.mutating || cmd.meta.DryRun != expected.dryRun || cmd.meta.Wait != expected.wait || len(cmd.meta.Endpoints) == 0 {
			t.Fatalf("%s meta = %+v", name, cmd.meta)
		}
	}
}
