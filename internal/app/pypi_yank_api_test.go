package app

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
)

func TestPyPIYankFileUpdatesBothSimpleRepresentations(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: "41111111-1111-4111-8111-111111111111", Name: "python-yank", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gateway_widget-1.0.0-py3-none-any.whl", "gateway_widget-1.0.0.tar.gz"} {
		_, err = store.PublishPyPIFile(t.Context(), repository.PyPIFile{RepositoryID: repo.ID, Project: "gateway-widget", Version: "1.0.0", Filename: name, Digest: "sha256:" + strings.Repeat("a", 64), Size: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	path := "/api/v2/repositories/" + repo.ID + "/pypi/projects/gateway-widget/versions/1.0.0/yank"
	request := httptest.NewRequest(http.MethodGet, path, nil)
	authorize(request, "admin-secret")
	read := httptest.NewRecorder()
	handler.ServeHTTP(read, request)
	if read.Code != http.StatusOK || read.Header().Get("ETag") == "" {
		t.Fatalf("yank state=%d %s, expected state and revision", read.Code, read.Body.String())
	}
	reason := "bad <abi> & wheel"
	request = httptest.NewRequest(http.MethodPatch, path, strings.NewReader(`{"filename":"gateway_widget-1.0.0-py3-none-any.whl","yanked":true,"reason":"bad <abi> & wheel"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", read.Header().Get("ETag"))
	authorize(request, "admin-secret")
	changed := httptest.NewRecorder()
	handler.ServeHTTP(changed, request)
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == read.Header().Get("ETag") {
		t.Fatalf("yank mutation=%d %s, expected updated state and revision", changed.Code, changed.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/pypi/python-yank/simple/gateway-widget/", nil)
	authorize(request, "admin-secret")
	html := httptest.NewRecorder()
	handler.ServeHTTP(html, request)
	if html.Code != http.StatusOK || strings.Count(html.Body.String(), `data-yanked="bad &lt;abi&gt; &amp; wheel"`) != 1 {
		t.Fatalf("file-scoped HTML yank=%d %s", html.Code, html.Body.String())
	}
	request.Header.Set("Accept", "application/vnd.pypi.simple.v1+json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var document struct {
		Files []struct {
			Filename string `json:"filename"`
			Yanked   any    `json:"yanked"`
		} `json:"files"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(document.Files) != 2 {
		t.Fatalf("JSON index=%d %s", response.Code, response.Body.String())
	}
	for _, file := range document.Files {
		if strings.HasSuffix(file.Filename, ".whl") && file.Yanked != reason {
			t.Fatalf("wheel reason=%v, want %q", file.Yanked, reason)
		}
		if strings.HasSuffix(file.Filename, ".tar.gz") && file.Yanked != false {
			t.Fatalf("sdist must remain selectable: %v", file.Yanked)
		}
	}
}

func TestPyPIYankMissingPortsRemainAuthorizedContractFailures(t *testing.T) {
	store, repo, _, path := newPyPIYankFixture(t)
	auth := testAuthenticator()
	auth.Users = store
	auth.LegacyReadPermissive = false
	reset, err := store.CreateUser(t.Context(), repository.User{ID: uuid.NewString(), Name: "yank-reset", Role: string(RoleAdmin), SecretHash: "synthetic-hash", MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"reader", "mutation"} {
		t.Run(missing, func(t *testing.T) {
			domain := pypiYankManagementHandler{managementScope: newManagementScope(store, store, store, store, auth, nil), files: store, yank: store}
			if missing == "reader" {
				domain.files = nil
			} else {
				domain.yank = nil
			}
			server := adminopenapi.HandlerWithOptions(managementServer{pypiYankManagementHandler: domain}, adminopenapi.StdHTTPServerOptions{BaseURL: "/api/v2", BaseRouter: openAPIServeMux{mux: http.NewServeMux()}})
			for _, method := range []string{http.MethodGet, http.MethodPatch} {
				for _, actor := range []struct {
					token      string
					get, patch int
				}{{"admin-secret", 501, 501}, {"resolver-secret", 501, 403}, {auth.IssueToken("outsider"), 403, 403}, {auth.IssuePrincipalToken(Principal{Actor: "pending", Role: RoleNone}), 403, 403}, {auth.IssueUserSession(reset.ID), 403, 403}, {"", 401, 401}} {
					want := actor.patch
					if method == http.MethodGet {
						want = actor.get
						if missing == "mutation" && want == 501 {
							want = 200
						}
					}
					r := httptest.NewRequest(method, "https://gateway.example.com"+path, strings.NewReader(`{"yanked":true}`))
					r.Header.Set("If-Match", `"`+strings.Repeat("a", 64)+`"`)
					if actor.token != "" {
						authorize(r, actor.token)
					}
					w := httptest.NewRecorder()
					server.ServeHTTP(w, r)
					if w.Code != want {
						t.Fatalf("%s missing %s=%d want%d %s", method, missing, w.Code, want, w.Body.String())
					}
					if want == 501 && !strings.Contains(w.Body.String(), `"code":"not_supported"`) {
						t.Fatalf("missing explicit capability=%s", w.Body.String())
					}
					if want != 200 && strings.Contains(w.Body.String(), "widget-1.0.tar.gz") {
						t.Fatalf("private file leaked=%s", w.Body.String())
					}
					route, params, routeErr := router.FindRoute(r)
					if routeErr != nil {
						t.Fatal(routeErr)
					}
					input := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route}
					if validateErr := openapi3filter.ValidateResponse(t.Context(), (&openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: w.Code, Header: w.Header(), Options: &openapi3filter.Options{IncludeResponseStatus: true}}).SetBodyBytes(w.Body.Bytes())); validateErr != nil {
						t.Fatalf("response contract=%v", validateErr)
					}
				}
			}
		})
	}
}

func TestPyPIYankProxyParsesCanonicalFileStates(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		yanked                  bool
		reason                  string
	}{
		{"HTML reason", "text/html", `<a href="widget-1.0.tar.gz#sha256=` + strings.Repeat("a", 64) + `" data-yanked="bad &lt;abi&gt;">wheel</a>`, true, "bad <abi>"},
		{"HTML empty", "text/html", `<a href="widget-1.0.tar.gz#sha256=` + strings.Repeat("a", 64) + `" data-yanked>sdist</a>`, true, ""},
		{"HTML absent", "text/html", `<a href="widget-1.0.tar.gz#sha256=` + strings.Repeat("a", 64) + `">sdist</a>`, false, ""},
		{"JSON reason", "application/vnd.pypi.simple.v1+json", `{"files":[{"filename":"widget-1.0.tar.gz","url":"widget-1.0.tar.gz","hashes":{"sha256":"` + strings.Repeat("a", 64) + `"},"yanked":"bad abi"}]}`, true, "bad abi"},
		{"JSON empty", "application/json", `{"files":[{"filename":"widget-1.0.tar.gz","url":"widget-1.0.tar.gz","hashes":{"sha256":"` + strings.Repeat("a", 64) + `"},"yanked":""}]}`, true, ""},
		{"JSON true", "application/json", `{"files":[{"filename":"widget-1.0.tar.gz","url":"widget-1.0.tar.gz","hashes":{"sha256":"` + strings.Repeat("a", 64) + `"},"yanked":true}]}`, true, ""},
		{"JSON false", "application/json", `{"files":[{"filename":"widget-1.0.tar.gz","url":"widget-1.0.tar.gz","hashes":{"sha256":"` + strings.Repeat("a", 64) + `"},"yanked":false}]}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := &http.Response{Header: http.Header{"Content-Type": {tc.contentType}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: httptest.NewRequest("GET", "https://pypi.example/simple/widget/", nil)}
			files, err := parsePyPISimpleResponse(response, "repo", "widget")
			if err != nil || len(files) != 1 || files[0].Yanked != tc.yanked || files[0].YankReason != tc.reason {
				t.Fatalf("files=%+v err=%v", files, err)
			}
		})
	}
}

func TestPyPIYankRevisionRejectsConcurrentUploadAndCompetingMutation(t *testing.T) {
	store, repo, handler, path := newPyPIYankFixture(t)
	old := pypiYankRequest(handler, http.MethodGet, path, "", "", "admin-secret").Header().Get("ETag")
	if _, err := store.PublishPyPIFile(t.Context(), repository.PyPIFile{RepositoryID: repo.ID, Project: "widget", Version: "1.0", Filename: "widget-1.0-py3-none-any.whl", Digest: "sha256:" + strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	if stale := pypiYankRequest(handler, http.MethodPatch, path, `{"yanked":true}`, old, "admin-secret"); stale.Code != 412 {
		t.Fatalf("stale upload=%d %s", stale.Code, stale.Body.String())
	}
	current := pypiYankRequest(handler, http.MethodGet, path, "", "", "admin-secret").Header().Get("ETag")
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses <- pypiYankRequest(handler, http.MethodPatch, path, fmt.Sprintf(`{"yanked":true,"reason":"reason%d"}`, i), current, "admin-secret").Code
		}()
	}
	wg.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[200] != 1 || counts[412] != 1 {
		t.Fatalf("concurrent mutations=%v", counts)
	}
	files, _ := store.ListPyPIProjectFiles(t.Context(), repo.ID, "widget")
	for _, file := range files {
		if !file.Yanked || file.YankReason != files[0].YankReason {
			t.Fatalf("partial version yank=%+v", files)
		}
	}
	audits, _ := store.ListAudits(t.Context(), repository.AuditQuery{Operation: "pypi.yank"})
	if len(audits) != 1 || audits[0].Evidence["changedFiles"] != "2" || audits[0].Actor != "alice" {
		t.Fatalf("success audits=%+v", audits)
	}
	revision := pypiYankRequest(handler, http.MethodGet, path, "", "", "admin-secret").Header().Get("ETag")
	unyank := pypiYankRequest(handler, http.MethodPatch, path, `{"yanked":false,"reason":"ignored on restore"}`, revision, "admin-secret")
	if unyank.Code != 200 || unyank.Header().Get("ETag") == old {
		t.Fatalf("unyank/ABA=%d %s", unyank.Code, unyank.Body.String())
	}
	files, _ = store.ListPyPIProjectFiles(t.Context(), repo.ID, "widget")
	for _, file := range files {
		if file.Yanked || file.YankReason != "" {
			t.Fatalf("unyank=%+v", files)
		}
	}
}

func TestPyPIYankAuthorizationAndInvalidInputDoNotMutate(t *testing.T) {
	store, repo, handler, path := newPyPIYankFixture(t)
	if _, err := store.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	read := pypiYankRequest(handler, http.MethodGet, path, "", "", "resolver-secret")
	if read.Code != 200 {
		t.Fatalf("granted reader=%d %s", read.Code, read.Body.String())
	}
	for _, token := range []string{"", "resolver-secret"} {
		w := pypiYankRequest(handler, http.MethodPatch, path, `{"yanked":true,"reason":"private"}`, read.Header().Get("ETag"), token)
		want := 403
		if token == "" {
			want = 401
		}
		if w.Code != want || strings.Contains(w.Body.String(), "widget-1.0") {
			t.Fatalf("denied=%d %s", w.Code, w.Body.String())
		}
	}
	for _, body := range []string{`{}`, `{"yanked":null}`, `{"yanked":true,"filename":""}`, `{"yanked":true,"filename":null}`, `{"yanked":true,"reason":null}`, `{"yanked":true,"extra":1}`, `{"yanked":true} {}`, `{"yanked":true,"reason":"bad\nline"}`, `{"yanked":true,"reason":"` + strings.Repeat("界", 342) + `"}`} {
		w := pypiYankRequest(handler, http.MethodPatch, path, body, read.Header().Get("ETag"), "admin-secret")
		if w.Code != 400 {
			t.Fatalf("invalid input=%d %s", w.Code, w.Body.String())
		}
	}
	audits, _ := store.ListAudits(t.Context(), repository.AuditQuery{Operation: "pypi.yank"})
	if len(audits) != 0 {
		t.Fatalf("false successful audit=%+v", audits)
	}
	// Write permission is sufficient; a platform role is not required.
	if _, err := store.ReplaceRepositoryGrants(t.Context(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}}}, "2"); err != nil {
		t.Fatal(err)
	}
	if w := pypiYankRequest(handler, http.MethodPatch, path, `{"yanked":true}`, read.Header().Get("ETag"), "resolver-secret"); w.Code != 200 {
		t.Fatalf("writer=%d %s", w.Code, w.Body.String())
	}
	if _, err := store.ReplaceRepositoryGrants(t.Context(), repo.ID, nil, "3"); err != nil {
		t.Fatal(err)
	}
	if w := pypiYankRequest(handler, http.MethodGet, path, "", "", "resolver-secret"); w.Code != 403 || strings.Contains(w.Body.String(), "widget-1.0") {
		t.Fatalf("revoked=%d %s", w.Code, w.Body.String())
	}
}

func newPyPIYankFixture(t *testing.T) (*repository.MemoryStore, repository.HostedRepository, http.Handler, string) {
	t.Helper()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(t.Context(), repository.HostedRepository{ID: uuid.NewString(), Name: "yank-fixture", Format: repository.FormatPyPI})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishPyPIFile(t.Context(), repository.PyPIFile{RepositoryID: repo.ID, Project: "widget", Version: "1.0", Filename: "widget-1.0.tar.gz", Digest: "sha256:" + strings.Repeat("a", 64), Size: 42}); err != nil {
		t.Fatal(err)
	}
	handler := newTestGateway(t, GatewayOptions{Store: store, Adapter: TestAdapter{}, Authenticator: testAuthenticator()})
	return store, repo, handler, "/api/v2/repositories/" + repo.ID + "/pypi/projects/widget/versions/1.0/yank"
}

func pypiYankRequest(handler http.Handler, method, path, body, etag, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	if token != "" {
		authorize(r, token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}
