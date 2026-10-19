package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
)

func TestArtifactManagementMissingCapabilitiesAreBoundedAndAuthorized(t *testing.T) {
	ctx := context.Background()
	spec, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "openapi", "management-runtime-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(spec)
	if err != nil {
		t.Fatal(err)
	}
	store := repository.NewMemoryStore()
	repos := map[repository.Format]repository.HostedRepository{}
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatMaven, repository.FormatOCI, repository.FormatConan, repository.FormatNPM, repository.FormatPyPI, repository.FormatGo, repository.FormatCargo, repository.FormatAPT} {
		repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cap-" + string(format), Format: format})
		if err != nil {
			t.Fatal(err)
		}
		repos[format] = repo
	}
	mavenSession, err := store.CreateMavenPublishSession(ctx, repository.MavenPublishSession{ID: uuid.NewString(), RepositoryID: repos[repository.FormatMaven].ID, Coordinate: "com.example:widget:1.0", State: "open", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	conanSession, err := store.CreateConanPublishSession(ctx, repository.ConanPublishSession{ID: uuid.NewString(), RepositoryID: repos[repository.FormatConan].ID, Kind: "recipe", Reference: "pkg/1.0@user/channel", RecipeRevision: "r1", State: "open", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	reset, err := store.CreateUser(ctx, repository.User{ID: uuid.NewString(), Name: "cap-reset", Role: string(RoleAdmin), SecretHash: "synthetic-password-hash", MustChangePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	ungranted, err := store.CreateUser(ctx, repository.User{ID: uuid.NewString(), Name: "cap-ungranted", Role: string(RoleMember), SecretHash: "synthetic-password-hash"})
	if err != nil {
		t.Fatal(err)
	}
	auth := testAuthenticator()
	auth.Users = store
	auth.LegacyReadPermissive = false
	auth.RepositoryReaders = map[string][]string{}
	scope := newManagementScope(store, store, store, &managementAuditFixture{}, auth, nil)
	options := artifactManagementOptions{Scope: scope, Repositories: store, Maven: store}
	// Conan session lookup is also independent from the optional publisher.
	domain := newArtifactManagementHandler(options)
	server := adminopenapi.HandlerWithOptions(managementServer{artifactManagementHandler: domain}, adminopenapi.StdHTTPServerOptions{BaseURL: "/api/v2", BaseRouter: openAPIServeMux{mux: http.NewServeMux()}})
	base := func(format repository.Format) string { return "/api/v2/repositories/" + repos[format].ID }
	digest := url.QueryEscape("sha256:" + strings.Repeat("a", 64))
	reference := url.QueryEscape("pkg/1.0@user/channel")
	tests := []struct {
		name, method, path string
		denied             int
	}{
		{"create Maven session", http.MethodPost, base(repository.FormatMaven) + "/publish-sessions", 403},
		{"list artifacts", http.MethodGet, base(repository.FormatRaw) + "/artifacts", 403},
		{"get artifact", http.MethodGet, base(repository.FormatMaven) + "/artifacts/" + uuid.NewString(), 403},
		{"delete artifact", http.MethodDelete, base(repository.FormatMaven) + "/artifacts/" + uuid.NewString(), 403},
		{"get intelligence", http.MethodGet, base(repository.FormatRaw) + "/artifact-intelligence?coordinate=docs/item.txt&digest=" + digest, 403},
		{"replace intelligence", http.MethodPut, base(repository.FormatRaw) + "/artifact-intelligence?coordinate=docs/item.txt&digest=" + digest, 403},
		{"OCI images", http.MethodGet, base(repository.FormatOCI) + "/oci/images", 403},
		{"OCI manifests", http.MethodGet, base(repository.FormatOCI) + "/oci/manifests?name=library/widget", 403},
		{"Conan references", http.MethodGet, base(repository.FormatConan) + "/conan/references", 403},
		{"Conan recipe revisions", http.MethodGet, base(repository.FormatConan) + "/conan/recipe-revisions?reference=" + reference, 403},
		{"Conan package revisions", http.MethodGet, base(repository.FormatConan) + "/conan/package-revisions?reference=" + reference + "&recipeRevision=r1&packageId=pid", 403},
		{"Conan package IDs", http.MethodGet, base(repository.FormatConan) + "/conan/package-ids?reference=" + reference + "&recipeRevision=r1", 403},
		{"delete Conan revision", http.MethodDelete, base(repository.FormatConan) + "/conan/package-revisions/r2?reference=" + reference + "&recipeRevision=r1&packageId=pid", 403},
		{"create Conan session", http.MethodPost, base(repository.FormatConan) + "/conan-publish-sessions", 403},
		{"get Conan session", http.MethodGet, "/api/v2/conan-publish-sessions/" + conanSession.ID, 404},
		{"upload Conan object", http.MethodPut, "/api/v2/conan-publish-sessions/" + conanSession.ID + "/objects/file.tgz", 404},
		{"commit Conan session", http.MethodPost, "/api/v2/conan-publish-sessions/" + conanSession.ID + ":commit", 404},
		{"list proxy cache", http.MethodGet, base(repository.FormatMaven) + "/cache/entries", 403},
		{"invalidate proxy cache", http.MethodPost, base(repository.FormatMaven) + "/cache/invalidate", 403},
		{"clear proxy negatives", http.MethodPost, base(repository.FormatMaven) + "/cache/negative:clear", 403},
		{"refresh proxy cache", http.MethodPost, base(repository.FormatMaven) + "/cache/refresh", 403},
		{"proxy health", http.MethodGet, base(repository.FormatMaven) + "/proxy/health", 403},
		{"get Maven session", http.MethodGet, "/api/v2/publish-sessions/" + mavenSession.ID, 403},
		{"upload Maven object", http.MethodPut, "/api/v2/publish-sessions/" + mavenSession.ID + "/objects/widget.pom", 403},
		{"commit Maven session", http.MethodPost, "/api/v2/publish-sessions/" + mavenSession.ID + ":commit", 403},
		{"browse", http.MethodGet, base(repository.FormatRaw) + "/browse", 403},
		{"identities", http.MethodGet, base(repository.FormatRaw) + "/artifact-identities?purpose=scan", 403},
		{"usage", http.MethodGet, base(repository.FormatRaw) + "/artifact-usage", 403},
		{"missing Group reader", http.MethodGet, "/api/v2/groups/" + uuid.NewString() + "/browse", 404},
	}
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatOCI, repository.FormatConan, repository.FormatNPM, repository.FormatPyPI, repository.FormatGo, repository.FormatCargo, repository.FormatAPT} {
		tests = append(tests, struct {
			name, method, path string
			denied             int
		}{"search " + string(format), http.MethodGet, base(format) + "/artifact-search", 403})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, actor := range []struct {
				token string
				want  int
			}{{"admin-secret", 501}, {auth.IssueUserSession(ungranted.ID), test.denied}, {auth.IssuePrincipalToken(Principal{Actor: "pending", Role: RoleNone}), 403}, {auth.IssueUserSession(reset.ID), 403}, {"", 401}} {
				request := httptest.NewRequest(test.method, "https://gateway.example.com"+test.path, strings.NewReader(`{}`))
				if actor.token != "" {
					authorize(request, actor.token)
				}
				request.Header.Set("Idempotency-Key", uuid.NewString())
				response := httptest.NewRecorder()
				func() {
					defer func() {
						if value := recover(); value != nil {
							t.Fatalf("missing capability panicked: %v", value)
						}
					}()
					server.ServeHTTP(response, request)
				}()
				if response.Code != actor.want {
					t.Fatalf("status=%d want=%d body=%s", response.Code, actor.want, response.Body.String())
				}
				if actor.want == 501 && !strings.Contains(response.Body.String(), `"code":"not_supported"`) {
					t.Fatalf("unavailable capability=%s", response.Body.String())
				}
				if actor.want == 501 {
					route, params, err := router.FindRoute(request)
					// The legacy schema router cannot match literal suffixes after
					// a path parameter. Resolve the two declared commit operations.
					if err != nil && strings.HasSuffix(request.URL.Path, ":commit") {
						path := "/publish-sessions/{sessionId}:commit"
						if strings.HasPrefix(request.URL.Path, "/api/v2/conan-publish-sessions/") {
							path = "/conan-publish-sessions/{sessionId}:commit"
						}
						item := spec.Paths.Value(path)
						route = &routers.Route{Spec: spec, Path: path, PathItem: item, Method: request.Method, Operation: item.Post}
						err = nil
					}
					if err != nil {
						t.Fatal(err)
					}
					input := &openapi3filter.RequestValidationInput{Request: request, PathParams: params, Route: route}
					if err := openapi3filter.ValidateResponse(ctx, (&openapi3filter.ResponseValidationInput{RequestValidationInput: input, Status: response.Code, Header: response.Header(), Options: &openapi3filter.Options{IncludeResponseStatus: true}}).SetBodyBytes(response.Body.Bytes())); err != nil {
						t.Fatalf("unavailable response contract: %v", err)
					}
				}
			}
		})
	}
}

func TestArtifactManagementMissingSearchAndBrowseNeverBecomeEmptySuccess(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cap-maven", Format: repository.FormatMaven, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "cap-group", Format: repository.FormatMaven, AnonymousRead: true, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}}}, "alice", "cap-group", "cap-group-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	auth := testAuthenticator()
	auth.LegacyReadPermissive = false
	auth.RepositoryReaders = map[string][]string{}
	options := artifactManagementOptions{Scope: newManagementScope(store, store, store, &managementAuditFixture{}, auth, nil), Repositories: store, Groups: store, Grants: store}
	server := adminopenapi.HandlerWithOptions(managementServer{artifactManagementHandler: newArtifactManagementHandler(options)}, adminopenapi.StdHTTPServerOptions{BaseURL: "/api/v2", BaseRouter: openAPIServeMux{mux: http.NewServeMux()}})
	for _, path := range []string{
		"/api/v2/repositories/" + repo.ID + "/maven/coordinates",
		"/api/v2/repositories/" + repo.ID + "/artifact-search?q=com.example",
		"/api/v2/artifact-search?q=com.example&format=maven",
		"/api/v2/repositories/" + group.ID + "/artifact-search?q=com.example",
		"/api/v2/groups/" + group.ID + "/browse",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			authorize(request, "admin-secret")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)
			if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), `"code":"not_supported"`) {
				t.Fatalf("missing recursive capability=%d %s", response.Code, response.Body.String())
			}
		})
	}
	// The global query continues to hide repositories this caller cannot read.
	request := httptest.NewRequest(http.MethodGet, "/api/v2/artifact-search?q=com.example&format=maven", nil)
	authorize(request, auth.IssueToken("user:ungranted"))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"items":[]`) {
		t.Fatalf("global visibility=%d %s", response.Code, response.Body.String())
	}
	// Grant-version lookup is independently optional; no Group data is exposed
	// to an ungranted member before reporting the unavailable configuration.
	options.Grants = nil
	server = adminopenapi.HandlerWithOptions(managementServer{artifactManagementHandler: newArtifactManagementHandler(options)}, adminopenapi.StdHTTPServerOptions{BaseURL: "/api/v2", BaseRouter: openAPIServeMux{mux: http.NewServeMux()}})
	request = httptest.NewRequest(http.MethodGet, "/api/v2/groups/"+group.ID+"/browse", nil)
	authorize(request, auth.IssueToken("user:ungranted"))
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("Group visibility before capability=%d %s", response.Code, response.Body.String())
	}
}

func TestArtifactManagementSessionLookupsAreSeparateFromPublishers(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	auth := testAuthenticator()
	auth.LegacyReadPermissive = false
	auth.RepositoryReaders = map[string][]string{}
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "cap-conan-session", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateConanPublishSession(ctx, repository.ConanPublishSession{ID: uuid.NewString(), RepositoryID: repo.ID, Reference: "pkg/1.0/user/channel", RecipeRevision: "r1", Kind: "recipe", State: "open", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	options := artifactManagementOptions{Scope: newManagementScope(store, store, store, &managementAuditFixture{}, auth, nil), Repositories: store, ConanSessions: store}
	server := adminopenapi.HandlerWithOptions(managementServer{artifactManagementHandler: newArtifactManagementHandler(options)}, adminopenapi.StdHTTPServerOptions{BaseURL: "/api/v2", BaseRouter: openAPIServeMux{mux: http.NewServeMux()}})
	call := func(token, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(r, token)
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	path := "/api/v2/conan-publish-sessions/" + session.ID
	reader := auth.IssueToken("user:session-reader")
	if response := call(reader, path); response.Code != http.StatusForbidden {
		t.Fatalf("ungranted session=%d %s", response.Code, response.Body.String())
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "user:session-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	if response := call(reader, path); response.Code != http.StatusNotImplemented {
		t.Fatalf("authorized without publisher=%d %s", response.Code, response.Body.String())
	}
	if response := call(reader, "/api/v2/conan-publish-sessions/"+uuid.NewString()); response.Code != http.StatusNotFound {
		t.Fatalf("missing session=%d %s", response.Code, response.Body.String())
	}
	// A missing Maven session reader refuses opaquely before it could attempt
	// a publisher operation; the administrator can inspect the configuration.
	for _, actor := range []struct {
		token string
		want  int
	}{{reader, 404}, {"admin-secret", 501}, {"", 401}} {
		response := call(actor.token, "/api/v2/publish-sessions/"+uuid.NewString())
		if response.Code != actor.want {
			t.Fatalf("missing session lookup=%d want=%d %s", response.Code, actor.want, response.Body.String())
		}
	}
}
