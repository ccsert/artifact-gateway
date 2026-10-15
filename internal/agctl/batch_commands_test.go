package agctl

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

const (
	testRepoID     = "00000000-0000-4000-8000-000000000001"
	testArtifactID = "00000000-0000-4000-a000-000000000001"
	testDigest     = "sha256:2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae"
)

type fakeResponse struct {
	status int
	body   string
}

// routeAPI is a fake Gateway answering from a route table keyed by
// "METHOD /path" (the query is recorded, not matched). Unknown routes answer
// 404 so a test fails loudly when a command calls something unexpected.
type routeAPI struct {
	server *httptest.Server
	prefix string

	mu       sync.Mutex
	routes   map[string]fakeResponse
	requests []recordedRequest
}

func newRouteAPI(t *testing.T, prefix string) *routeAPI {
	t.Helper()
	isolatedCLI(t)
	api := &routeAPI{routes: map[string]fakeResponse{}, prefix: prefix}
	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		api.mu.Lock()
		defer api.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, api.prefix)
		api.requests = append(api.requests, recordedRequest{Method: r.Method, Path: path, Query: r.URL.RawQuery, Body: string(body)})
		if r.Header.Get("Authorization") != "Bearer "+repoTestToken && path != "/livez" && path != "/readyz" {
			writeProblem(w, http.StatusUnauthorized, "access_denied", "authentication required")
			return
		}
		response, ok := api.routes[r.Method+" "+path]
		if !ok {
			writeProblem(w, http.StatusNotFound, "not_found", "no route "+r.Method+" "+path)
			return
		}
		if response.status >= 400 {
			var problem struct{ Code, Message string }
			_ = json.Unmarshal([]byte(response.body), &problem)
			writeProblem(w, response.status, problem.Code, problem.Message)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		_, _ = io.WriteString(w, response.body)
	}))
	t.Cleanup(api.server.Close)
	t.Setenv("AGCTL_SERVER", api.server.URL+prefix)
	t.Setenv("AGCTL_TOKEN", repoTestToken)
	return api
}

func (api *routeAPI) on(method, path string, status int, body string) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.routes[method+" "+path] = fakeResponse{status: status, body: body}
}

func (api *routeAPI) recorded() []recordedRequest {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]recordedRequest(nil), api.requests...)
}

func (api *routeAPI) count(method string) int {
	n := 0
	for _, request := range api.recorded() {
		if request.Method == method {
			n++
		}
	}
	return n
}

func expectUsage(t *testing.T, api *routeAPI, cases ...[]string) {
	t.Helper()
	for _, args := range cases {
		if code, stdout, stderr := runCLI(t, args...); code != ExitUsage {
			t.Fatalf("%v exit = %d, want %d\nstdout = %s\nstderr = %s", args, code, ExitUsage, stdout, stderr)
		}
	}
	if requests := api.recorded(); len(requests) != 0 {
		t.Fatalf("usage errors must not send requests: %+v", requests)
	}
}

const repoBase = "/api/v2/repositories/" + testRepoID

func TestArtifactSearchValidatesBeforeRequest(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"artifact", "search"},
		[]string{"artifact", "search", "--global", "--repo", testRepoID},
		[]string{"artifact", "search", "--global"},
		[]string{"artifact", "search", "--global", "--q", "x", "--format", "rpm"},
		[]string{"artifact", "search", "--repo", testRepoID, "--format", "npm"},
		[]string{"artifact", "search", "--repo", testRepoID, "--match", "fuzzy"},
	)
}

func TestArtifactSearchGlobalReportsSearchedRepositories(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", "/api/v2/artifact-search", 200, `{"items":[{"repositoryId":"`+testRepoID+`","repositoryName":"npm-hosted","format":"npm","coordinate":"left-pad","version":"1.3.0","matchKind":"name","size":2048,"digest":"`+testDigest+`"}],"searchedRepositories":3,"nextPageToken":"t-2"}`)
	code, stdout, stderr := runCLI(t, "artifact", "search", "--global", "--q", "left", "--format", "npm", "--match", "prefix", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr = %s", code, stderr)
	}
	assertGolden(t, "artifact_search_global", stdout)
	request := api.recorded()[0]
	if request.Query != "format=npm&match=prefix&pageSize=50&q=left" {
		t.Fatalf("query = %s", request.Query)
	}
}

func TestArtifactSearchInRepository(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase+"/artifact-search", 200, `{"items":[{"coordinate":"releases/app.txt","size":12}]}`)
	code, stdout, _ := runCLI(t, "artifact", "search", "--repo", testRepoID, "--q", "releases/", "--page-size", "10")
	if code != ExitOK || !strings.Contains(stdout, "releases/app.txt") || !strings.Contains(stdout, "12 B") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if query := api.recorded()[0].Query; query != "pageSize=10&q=releases%2F" {
		t.Fatalf("query = %s", query)
	}
}

func TestBrowseCommandsShareTheEndpoint(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase+"/browse", 200, `{"items":[{"id":"n-1","kind":"directory","name":"releases","hasChildren":true}]}`)
	for _, args := range [][]string{
		{"artifact", "browse", "--repo", testRepoID, "--parent", "root-1"},
		{"repo", "browse", testRepoID, "--parent", "root-1"},
	} {
		code, stdout, _ := runCLI(t, args...)
		if code != ExitOK || !strings.Contains(stdout, "releases/") {
			t.Fatalf("%v exit = %d, stdout = %s", args, code, stdout)
		}
	}
	for _, request := range api.recorded() {
		if request.Path != repoBase+"/browse" || request.Query != "pageSize=50&parent=root-1" {
			t.Fatalf("request = %+v", request)
		}
	}
	expectUsage(t, newRouteAPI(t, ""), []string{"artifact", "browse"})
}

func TestGroupBrowseKeepsGroupContext(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", "/api/v2/groups/"+testRepoID+"/browse", 200, `{"groupId":"`+testRepoID+`","groupName":"raw-all","format":"raw","candidates":[{"repositoryId":"`+testRepoID+`","repositoryName":"raw-hosted","type":"hosted","resolutionOrder":0}],"items":[{"id":"n-1","kind":"asset","name":"app.txt","hasChildren":false,"size":12,"sourceRepositoryName":"raw-hosted"}]}`)
	code, stdout, _ := runCLI(t, "group", "browse", testRepoID, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "group_browse", stdout)
}

func TestArtifactListIsNotPaginated(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase+"/artifacts", 200, `{"items":[{"id":"`+testArtifactID+`","coordinate":"com.example:demo:1.0.0","digest":"`+testDigest+`","createdAt":"2026-10-15T08:00:00Z","state":"visible"}]}`)
	code, stdout, _ := runCLI(t, "artifact", "list", "--repo", testRepoID, "--json")
	envelope := decodeEnvelope(t, stdout)
	if code != ExitOK || !strings.Contains(string(envelope.Meta), `"mode": "none"`) || api.recorded()[0].Query != "" {
		t.Fatalf("exit = %d, stdout = %s, requests = %+v", code, stdout, api.recorded())
	}
}

func TestArtifactShowReportsWhatItCouldNotRead(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase+"/artifacts/"+testArtifactID, 200, `{"id":"`+testArtifactID+`","coordinate":"com.example:demo:1.0.0","digest":"`+testDigest+`","createdAt":"2026-10-15T08:00:00Z","state":"visible","futureField":"kept"}`)
	api.on("GET", repoBase+"/artifact-intelligence", 404, `{"code":"not_found","message":"artifact intelligence not found"}`)
	api.on("GET", repoBase+"/artifact-quarantine", 403, `{"code":"access_denied","message":"denied"}`)
	code, stdout, _ := runCLI(t, "artifact", "show", testArtifactID, "--repo", testRepoID, "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "artifact_show_partial", stdout)
	for _, request := range api.recorded()[1:] {
		if request.Query != "coordinate=com.example%3Ademo%3A1.0.0&digest="+strings.ReplaceAll(testDigest, ":", "%3A") {
			t.Fatalf("aspect query = %s", request.Query)
		}
	}
	code, stdout, _ = runCLI(t, "artifact", "show", testArtifactID, "--repo", testRepoID)
	if code != ExitOK || !strings.Contains(stdout, "none recorded") || !strings.Contains(stdout, "not visible to this credential") || strings.Contains(stdout, "healthy") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestArtifactDeleteIsConfirmedAndRetriedIdempotently(t *testing.T) {
	api := newRouteAPI(t, "")
	path := repoBase + "/artifacts/" + testArtifactID
	expectUsage(t, api,
		[]string{"artifact", "delete", testArtifactID, "--yes"},
		[]string{"artifact", "delete", testArtifactID, "--repo", testRepoID},
	)
	api.on("GET", path, 200, `{"id":"`+testArtifactID+`","coordinate":"com.example:demo:1.0.0","digest":"`+testDigest+`","createdAt":"2026-10-15T08:00:00Z","state":"visible"}`)
	code, stdout, _ := runCLI(t, "artifact", "delete", testArtifactID, "--repo", testRepoID, "--dry-run", "--json")
	if code != ExitOK || api.count("DELETE") != 0 || !strings.Contains(stdout, `"mode": "client-plan"`) {
		t.Fatalf("dry run exit = %d, stdout = %s", code, stdout)
	}

	api.on("DELETE", path, 503, `{"code":"unavailable","message":"try again"}`)
	code, stdout, _ = runCLI(t, "artifact", "delete", testArtifactID, "--repo", testRepoID, "--yes", "--json")
	var data unknownOutcome
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	// Tombstoning is idempotent, so a 503 is retried; when every attempt
	// fails the outcome stays unknown.
	if code == ExitOK || data.Outcome != "unknown" || api.count("DELETE") != 1+clientMaxRetries || !strings.HasPrefix(data.ReadBack, "agctl artifact show") {
		t.Fatalf("exit = %d, deletes = %d, stdout = %s", code, api.count("DELETE"), stdout)
	}

	api.on("DELETE", path, 202, ``)
	code, stdout, _ = runCLI(t, "artifact", "delete", testArtifactID, "--repo", testRepoID, "--yes", "--json")
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &data)
	if code == ExitOK || data.Outcome != "unknown" {
		t.Fatalf("an empty 202 must be an unknown outcome: exit = %d, stdout = %s", code, stdout)
	}

	api.on("DELETE", path, 202, `{"id":"`+testArtifactID+`","state":"pending"}`)
	code, stdout, _ = runCLI(t, "artifact", "delete", testArtifactID, "--repo", testRepoID, "--yes", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"serverState": "pending"`) || !strings.Contains(stdout, `"status": "accepted"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestArtifactIdentitiesIsBounded(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"artifact", "identities", "--repo", testRepoID},
		[]string{"artifact", "identities", "--repo", testRepoID, "--purpose", "publish"},
		[]string{"artifact", "identities", "--repo", testRepoID, "--purpose", "scan", "--limit", "201"},
	)
	api.on("GET", repoBase+"/artifact-identities", 200, `{"items":[{"coordinate":"a","digest":"`+testDigest+`","publishedAt":"2026-10-15T08:00:00Z"},{"coordinate":"b","digest":"`+testDigest+`","publishedAt":"2026-10-15T08:00:00Z"}]}`)
	code, stdout, stderr := runCLI(t, "artifact", "identities", "--repo", testRepoID, "--purpose", "scan", "--limit", "2")
	if code != ExitOK || !strings.Contains(stderr, "the window of 2 is full") {
		t.Fatalf("a full window must say more may exist: exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	code, stdout, _ = runCLI(t, "artifact", "identities", "--repo", testRepoID, "--purpose", "distribution", "--limit", "5", "--json")
	meta := string(decodeEnvelope(t, stdout).Meta)
	if code != ExitOK || !strings.Contains(meta, `"mode": "bounded"`) || !strings.Contains(meta, `"complete": true`) {
		t.Fatalf("exit = %d, meta = %s", code, meta)
	}
}

func TestArtifactUsageReportsOffsetWindow(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase+"/artifact-usage", 200, `{"repositoryId":"`+testRepoID+`","generatedAt":"2026-10-15T08:00:00Z","totalCount":3,"totals":{"downloadCount":9,"totalBytes":4096,"resources":3},"items":[{"format":"raw","resource":"a.txt","downloadCount":4,"totalBytes":2048,"firstDownloadedAt":"2026-10-14T08:00:00Z","lastDownloadedAt":"2026-10-15T08:00:00Z"}]}`)
	code, stdout, _ := runCLI(t, "artifact", "usage", "--repo", testRepoID, "--limit", "1", "--offset", "1", "--json")
	meta := string(decodeEnvelope(t, stdout).Meta)
	if code != ExitOK || !strings.Contains(meta, `"mode": "offset"`) || !strings.Contains(meta, `"total": 3`) || !strings.Contains(meta, `"complete": false`) {
		t.Fatalf("exit = %d, meta = %s", code, meta)
	}
	code, stdout, _ = runCLI(t, "artifact", "usage", "--repo", testRepoID, "--limit", "1", "--offset", "1")
	if code != ExitOK || !strings.Contains(stdout, "continue with --offset 2") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	expectUsage(t, newRouteAPI(t, ""), []string{"artifact", "usage", "--repo", testRepoID, "--limit", "501"})
}

func TestArtifactRecordsPassServerFieldsThrough(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"artifact", "intelligence", "show", "--repo", testRepoID, "--digest", testDigest},
		[]string{"artifact", "quarantine", "show", "--repo", testRepoID, "--coordinate", "a"},
	)
	api.on("GET", repoBase+"/artifact-quarantine", 200, `{"repositoryId":"`+testRepoID+`","format":"raw","coordinate":"a","digest":"`+testDigest+`","state":"quarantined","reason":"cve","version":"2","updatedBy":"admin","updatedAt":"2026-10-15T08:00:00Z","futureField":true}`)
	code, stdout, _ := runCLI(t, "artifact", "quarantine", "show", "--repo", testRepoID, "--coordinate", "a", "--digest", testDigest, "--json")
	if code != ExitOK || !strings.Contains(stdout, `"futureField": true`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "artifact", "quarantine", "show", "--repo", testRepoID, "--coordinate", "a", "--digest", testDigest)
	if code != ExitOK || !strings.Contains(stdout, "quarantined") || !strings.Contains(stdout, "cve") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestFormatViews(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api, []string{"oci", "manifests", "--repo", testRepoID}, []string{"maven", "coordinates"})
	api.on("GET", repoBase+"/oci/manifests", 200, `{"items":[{"digest":"`+testDigest+`","mediaType":"application/vnd.oci.image.manifest.v1+json","size":512,"tags":["1.0","latest"],"createdAt":"2026-10-15T08:00:00Z"}]}`)
	api.on("GET", repoBase+"/oci/images", 200, `{"items":[{"name":"team/app"}],"nextPageToken":"t-2"}`)
	api.on("GET", repoBase+"/maven/coordinates", 200, `{"items":[]}`)
	code, stdout, _ := runCLI(t, "oci", "manifests", "--repo", testRepoID, "--name", "team/app")
	if code != ExitOK || !strings.Contains(stdout, "1.0,latest") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, stderr := runCLI(t, "oci", "images", "--repo", testRepoID, "--q", "team/")
	if code != ExitOK || !strings.Contains(stdout, "team/app") || !strings.Contains(stderr, "re-run the same command with --cursor t-2") {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	code, stdout, _ = runCLI(t, "maven", "coordinates", "--repo", testRepoID)
	if code != ExitOK || !strings.Contains(stdout, "no coordinates") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	for _, request := range api.recorded() {
		if request.Path == repoBase+"/oci/manifests" && request.Query != "name=team%2Fapp&pageSize=50" {
			t.Fatalf("manifests query = %s", request.Query)
		}
	}
}

func TestRepoCapabilities(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase+"/capabilities", 200, `{"format":"raw","type":"hosted","operations":["read","publish","browse","delete"],"artifactScanning":false,"publicationScanning":false}`)
	code, stdout, _ := runCLI(t, "repo", "capabilities", testRepoID)
	if code != ExitOK || !strings.Contains(stdout, "read, publish, browse, delete") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestRepoConnectSnapshots(t *testing.T) {
	for _, tc := range []struct{ format, kind string }{
		{"npm", "hosted"}, {"npm", "proxy"}, {"pypi", "hosted"}, {"maven", "hosted"}, {"go", "proxy"},
		{"oci", "hosted"}, {"raw", "hosted"}, {"conan", "proxy"}, {"apt", "hosted"}, {"cargo", "hosted"},
	} {
		t.Run(tc.format+"-"+tc.kind, func(t *testing.T) {
			api := newRouteAPI(t, "/gw")
			name := tc.format + "-" + tc.kind
			api.on("GET", repoBase, 200, `{"id":"`+testRepoID+`","name":"`+name+`","format":"`+tc.format+`","type":"`+tc.kind+`","state":"active","version":"1","anonymousRead":false,"mavenStrictPublication":false}`)
			code, stdout, stderr := runCLI(t, "repo", "connect", testRepoID)
			if code != ExitOK {
				t.Fatalf("exit = %d, stderr = %s", code, stderr)
			}
			if strings.Contains(stdout, repoTestToken) {
				t.Fatalf("the configured token leaked into a snippet: %s", stdout)
			}
			host := strings.TrimPrefix(api.server.URL, "http://")
			server, err := url.Parse(api.server.URL)
			if err != nil {
				t.Fatal(err)
			}
			assertGolden(t, "repo_connect_"+tc.format+"_"+tc.kind+".txt", stdout, host, "gateway.test", server.Hostname(), "gateway.test")
		})
	}
}

func TestRepoConnectJSONAndInactive(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", repoBase, 200, `{"id":"`+testRepoID+`","name":"raw-hosted","format":"raw","type":"hosted","state":"deleting","version":"2","anonymousRead":false,"mavenStrictPublication":false}`)
	code, stdout, _ := runCLI(t, "repo", "connect", testRepoID, "--json")
	var guide connectGuide
	_ = json.Unmarshal(decodeEnvelope(t, stdout).Data, &guide)
	if code != ExitOK || guide.Credential != tokenPlaceholder || len(guide.Snippets) != 2 || len(guide.Notes) != 1 || !strings.Contains(guide.Notes[0], "deleting") {
		t.Fatalf("exit = %d, guide = %+v", code, guide)
	}
}

const auditRecords = `[{"outcome":"access_denied","occurredAt":"2026-10-15T08:00:00Z","actor":"svc-ci","operation":"read","format":"raw","repository":"raw-hosted","status":403,"resource":"releases/app.txt","requestId":"req-1"},{"outcome":"success","occurredAt":"2026-10-15T07:00:00Z","actor":"admin","operation":"create","format":"management","resource":"repositories/x, \"quoted\""}]`

func TestAuditListPagesWithFilters(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", "/api/v2/audits/page", 200, `{"items":`+auditRecords+`,"nextPageToken":"t-2"}`)
	expectUsage(t, api,
		[]string{"audit", "list", "--from", "yesterday"},
		[]string{"audit", "list", "--bounded", "--from", "2026-10-15T00:00:00Z"},
		[]string{"audit", "list", "--bounded", "--all"},
		[]string{"audit", "list", "--limit", "10"},
		[]string{"audit", "list", "--bounded", "--limit", "501"},
		[]string{"audit", "list", "--csv", "--json"},
	)
	code, stdout, stderr := runCLI(t, "audit", "list", "--actor", "svc-ci", "--outcome", "access_denied", "--from", "2026-10-15T00:00:00Z")
	if code != ExitOK || !strings.Contains(stdout, "svc-ci") || !strings.Contains(stderr, "--cursor t-2") {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
	if query := api.recorded()[0].Query; query != "actor=svc-ci&from=2026-10-15T00%3A00%3A00Z&outcome=access_denied&pageSize=50" {
		t.Fatalf("query = %s", query)
	}
}

func TestAuditListCSVAndBounded(t *testing.T) {
	api := newRouteAPI(t, "")
	api.on("GET", "/api/v2/audits", 200, auditRecords)
	code, stdout, _ := runCLI(t, "audit", "list", "--bounded", "--limit", "2", "--csv")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "audit_list_bounded.csv", strings.TrimSpace(stdout)+"\n")
	if query := api.recorded()[0].Query; query != "limit=2" {
		t.Fatalf("query = %s", query)
	}
	code, stdout, _ = runCLI(t, "audit", "list", "--bounded", "--limit", "2", "--json")
	meta := string(decodeEnvelope(t, stdout).Meta)
	if code != ExitOK || !strings.Contains(meta, `"mode": "bounded"`) || strings.Contains(meta, "complete") {
		t.Fatalf("a full bounded window is not complete: exit = %d, meta = %s", code, meta)
	}
}

const healthyDiagnostics = `{"generatedAt":"2026-10-15T08:00:00Z","build":{"version":"0.9.0","goVersion":"go1.25","revision":"abc"},"runtime":{"instanceId":"i-1","roles":["api"],"workerFormats":[],"workerKinds":[]},"dependencies":[{"name":"postgres","status":"reachable"},{"name":"smtp","status":"not_configured"}],"queues":[],"nodes":{"status":"healthy","online":1,"stale":0,"offline":0,"issues":[]}}`

func statusAPI(t *testing.T) *routeAPI {
	api := newRouteAPI(t, "")
	api.on("GET", "/livez", 204, "")
	api.on("GET", "/readyz", 204, "")
	api.on("GET", "/api/v2/diagnostics", 200, healthyDiagnostics)
	return api
}

func TestStatusHealthy(t *testing.T) {
	api := statusAPI(t)
	code, stdout, _ := runCLI(t, "status", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "status_healthy", stdout, api.server.URL, "http://gateway.test")
}

func TestStatusUnhealthyExitsNonZeroWithReport(t *testing.T) {
	api := statusAPI(t)
	api.on("GET", "/readyz", 503, `{"code":"not_ready","message":"database unavailable"}`)
	api.on("GET", "/api/v2/diagnostics", 200, strings.Replace(healthyDiagnostics, `"status":"reachable"`, `"status":"unreachable","detail":"connection refused"`, 1))
	code, stdout, _ := runCLI(t, "status", "--json")
	envelope := decodeEnvelope(t, stdout)
	if code != ExitFailure || envelope.Error.Code != "unhealthy" || !strings.Contains(string(envelope.Data), `"overall": "unhealthy"`) || !strings.Contains(string(envelope.Data), "connection refused") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, stderr := runCLI(t, "status")
	if code != ExitFailure || !strings.Contains(stdout, "readyz") || !strings.Contains(stderr, "the Gateway is unhealthy") {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, stdout, stderr)
	}
}

func TestStatusUnknownAndDegradedNeedStrict(t *testing.T) {
	api := statusAPI(t)
	api.on("GET", "/api/v2/diagnostics", 403, `{"code":"access_denied","message":"denied"}`)
	code, stdout, _ := runCLI(t, "status", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"overall": "unknown"`) {
		t.Fatalf("unreadable diagnostics are unknown, not healthy: exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "status", "--strict", "--json")
	if code != ExitFailure || decodeEnvelope(t, stdout).Error.Code != "unknown" {
		t.Fatalf("--strict exit = %d, stdout = %s", code, stdout)
	}

	api.on("GET", "/api/v2/diagnostics", 200, strings.Replace(healthyDiagnostics, `"queues":[]`, `"queues":[{"kind":"replication","format":"maven","state":"failed","count":2}],"scanner":{"name":"trivy","formats":["oci"],"status":"healthy","detail":"","checkedAt":"2026-10-15T08:00:00Z","databaseFreshness":"stale","databaseMaxAgeSeconds":86400}`, 1))
	code, stdout, _ = runCLI(t, "status", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"overall": "degraded"`) || !strings.Contains(stdout, "2 failed jobs") || !strings.Contains(stdout, "database stale") {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	if code, _, _ = runCLI(t, "status", "--strict"); code != ExitFailure {
		t.Fatalf("--strict degraded exit = %d", code)
	}
}

func TestStatusWithoutCredentials(t *testing.T) {
	statusAPI(t)
	t.Setenv("AGCTL_TOKEN", "")
	code, stdout, _ := runCLI(t, "status", "--json")
	if code != ExitOK || !strings.Contains(stdout, "no credentials configured") || !strings.Contains(stdout, `"overall": "unknown"`) {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
}

func TestDiagnosticsRuntimeAndOverview(t *testing.T) {
	api := statusAPI(t)
	api.on("GET", "/api/v2/diagnostics", 200, strings.Replace(healthyDiagnostics, `"queues":[]`, `"queues":[],"futureField":1`, 1))
	api.on("GET", "/api/v2/runtime/nodes", 200, `{"items":[{"instanceId":"i-1","sessionId":"s-1","roles":["api","worker"],"workerFormats":[],"workerKinds":[],"startedAt":"2026-10-15T07:00:00Z","lastSeenAt":"2026-10-15T08:00:00Z","status":"online"}],"health":{"status":"healthy","online":1,"stale":0,"offline":0,"issues":[]},"currentSessionId":"s-1","releaseSource":"not_configured"}`)
	api.on("GET", "/api/v2/overview-statistics", 200, `{"generatedAt":"2026-10-15T08:00:00Z","totals":{"requests":{"oneDay":5,"sevenDays":9,"thirtyDays":20},"denied":{"oneDay":1,"sevenDays":1,"thirtyDays":2},"objectCount":3,"usedBytes":4096},"repositories":[{"repositoryId":"`+testRepoID+`","name":"raw-hosted","format":"raw","requests":{"oneDay":5,"sevenDays":9,"thirtyDays":20},"denied":{"oneDay":1,"sevenDays":1,"thirtyDays":2},"objectCount":3,"usedBytes":4096}]}`)
	code, stdout, _ := runCLI(t, "diagnostics", "--json")
	if code != ExitOK || !strings.Contains(stdout, `"futureField": 1`) {
		t.Fatalf("diagnostics exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "diagnostics")
	if code != ExitOK || !strings.Contains(stdout, "dependency postgres") || !strings.Contains(stdout, "local capacity") {
		t.Fatalf("diagnostics exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "runtime", "nodes")
	if code != ExitOK || !strings.Contains(stdout, "s-1 (this response)") || !strings.Contains(stdout, "api,worker") {
		t.Fatalf("nodes exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "overview", "show")
	if code != ExitOK || !strings.Contains(stdout, "5 / 9 / 20") || !strings.Contains(stdout, "4.0 KiB") {
		t.Fatalf("overview exit = %d, stdout = %s", code, stdout)
	}
}

const lifecycleJob = `{"id":"job-1","kind":"retention","state":"completed","createdAt":"2026-10-15T08:00:00Z","attempts":1,"maxAttempts":3,"progressCurrent":4,"progressTotal":4}`

func TestLifecycleJobs(t *testing.T) {
	api := newRouteAPI(t, "")
	expectUsage(t, api,
		[]string{"lifecycle-job", "list", "--repo", testRepoID, "--limit", "5"},
		[]string{"lifecycle-job", "list", "--limit", "0"},
		[]string{"lifecycle-job", "show"},
	)
	api.on("GET", "/api/v2/lifecycle-jobs", 200, `[{"repositoryId":"`+testRepoID+`","repositoryName":"raw-hosted","job":`+lifecycleJob+`}]`)
	api.on("GET", repoBase+"/lifecycle-jobs", 200, `[`+lifecycleJob+`]`)
	code, stdout, _ := runCLI(t, "lifecycle-job", "list", "--limit", "10", "--json")
	if code != ExitOK {
		t.Fatalf("exit = %d, stdout = %s", code, stdout)
	}
	assertGolden(t, "lifecycle_job_list", stdout)
	if query := api.recorded()[0].Query; query != "limit=10" {
		t.Fatalf("query = %s", query)
	}
	code, stdout, _ = runCLI(t, "lifecycle-job", "show", "job-1", "--repo", testRepoID)
	if code != ExitOK || !strings.Contains(stdout, "retention") || !strings.Contains(stdout, "4/4") {
		t.Fatalf("show exit = %d, stdout = %s", code, stdout)
	}
	code, stdout, _ = runCLI(t, "lifecycle-job", "show", "job-9", "--json")
	envelope := decodeEnvelope(t, stdout)
	if code != ExitNotFound || !strings.Contains(envelope.Error.Message, "newest jobs") {
		t.Fatalf("missing job exit = %d, stdout = %s", code, stdout)
	}
}

func TestBatchCommandsAreRegisteredHonestly(t *testing.T) {
	mutating := map[string]bool{"artifact delete": true}
	for _, name := range []string{
		"group browse", "repo browse", "repo capabilities", "repo connect",
		"artifact search", "artifact list", "artifact browse", "artifact show", "artifact delete",
		"artifact identities", "artifact usage", "artifact intelligence show", "artifact quarantine show",
		"oci images", "oci manifests", "maven coordinates",
		"audit list", "status", "diagnostics", "runtime nodes", "overview show", "lifecycle-job list", "lifecycle-job show",
	} {
		cmd, ok := lookupCommand(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if cmd.meta.Mutating != mutating[name] || len(cmd.meta.Endpoints) == 0 || cmd.meta.Wait {
			t.Fatalf("%s meta = %+v", name, cmd.meta)
		}
		if mutating[name] && (cmd.meta.DryRun != "client-plan" || !cmd.meta.Retry) {
			t.Fatalf("%s must plan and retry its idempotent tombstone: %+v", name, cmd.meta)
		}
		if !mutating[name] && (cmd.meta.DryRun != "unsupported" || !cmd.meta.Retry) {
			t.Fatalf("%s is a retried read: %+v", name, cmd.meta)
		}
	}
}
