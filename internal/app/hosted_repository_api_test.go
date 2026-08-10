package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	adminopenapi "github.com/artifact-gateway/artifact-gateway/internal/admin/openapi"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHostedRepositoryManagementLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"releases","format":"maven"}`))
	authorize(request, "admin-secret")
	request.Header.Set("Idempotency-Key", "create-releases")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	if !strings.Contains(created.Body.String(), `"state":"active"`) {
		t.Fatalf("created=%s", created.Body.String())
	}
	id := strings.Split(strings.Split(created.Body.String(), `"id":"`)[1], `"`)[0]
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "releases") {
		t.Fatalf("list=%d body=%s", listed.Code, listed.Body.String())
	}
	disable := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+id, nil)
	authorize(disable, "admin-secret")
	disabled := httptest.NewRecorder()
	handler.ServeHTTP(disabled, disable)
	if disabled.Code != http.StatusAccepted || !strings.Contains(disabled.Body.String(), `"state":"pending"`) {
		t.Fatalf("disable=%d body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestRepositoryCapabilitiesReportImplementedFormatOperations(t *testing.T) {
	store := repository.NewMemoryStore()
	oci, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-oci", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	conan, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-conan", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	maven, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-maven", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "capabilities-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	if _, err = store.ReplaceRepositoryGrants(context.Background(), conan.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+conan.ID+"/capabilities", nil)
	authorize(request, authenticator.IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"format":"conan"`) || !strings.Contains(response.Body.String(), `"restore"`) || !strings.Contains(response.Body.String(), `"retain"`) || !strings.Contains(response.Body.String(), `"promote"`) || !strings.Contains(response.Body.String(), `"replicate"`) {
		t.Fatalf("Conan capabilities=%d %s", response.Code, response.Body.String())
	}
	adminRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+maven.ID+"/capabilities", nil)
	authorize(adminRequest, "admin-secret")
	adminResponse := httptest.NewRecorder()
	handler.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK || !strings.Contains(adminResponse.Body.String(), `"retain"`) || !strings.Contains(adminResponse.Body.String(), `"restore"`) {
		t.Fatalf("Maven capabilities=%d %s", adminResponse.Code, adminResponse.Body.String())
	}
	ociRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+oci.ID+"/capabilities", nil)
	authorize(ociRequest, "admin-secret")
	ociResponse := httptest.NewRecorder()
	handler.ServeHTTP(ociResponse, ociRequest)
	if ociResponse.Code != http.StatusOK || !strings.Contains(ociResponse.Body.String(), `"restore"`) {
		t.Fatalf("OCI capabilities=%d %s", ociResponse.Code, ociResponse.Body.String())
	}
	rawRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+raw.ID+"/capabilities", nil)
	authorize(rawRequest, "admin-secret")
	rawResponse := httptest.NewRecorder()
	handler.ServeHTTP(rawResponse, rawRequest)
	if rawResponse.Code != http.StatusOK || !strings.Contains(rawResponse.Body.String(), `"restore"`) || !strings.Contains(rawResponse.Body.String(), `"retain"`) || !strings.Contains(rawResponse.Body.String(), `"promote"`) || !strings.Contains(rawResponse.Body.String(), `"replicate"`) {
		t.Fatalf("Raw capabilities=%d %s", rawResponse.Code, rawResponse.Body.String())
	}
}

func TestHostedRepositoryManagementCreatesProxyRepository(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := func(body, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}

	created := create(`{"name":"raw-proxy","format":"raw","type":"proxy","endpoint":"https://upstream.example","allowedHosts":["upstream.example","cdn.example"]}`, "create-raw-proxy")
	if created.Code != http.StatusCreated {
		t.Fatalf("create proxy=%d body=%s", created.Code, created.Body.String())
	}
	for _, fragment := range []string{`"type":"proxy"`, `"endpoint":"https://upstream.example"`, `"allowedHosts":["upstream.example","cdn.example"]`} {
		if !strings.Contains(created.Body.String(), fragment) {
			t.Fatalf("proxy response missing %s: %s", fragment, created.Body.String())
		}
	}
	id := strings.Split(strings.Split(created.Body.String(), `"id":"`)[1], `"`)[0]

	// The proxy shape persists through the read path.
	get := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+id, nil)
	authorize(get, "admin-secret")
	loaded := httptest.NewRecorder()
	handler.ServeHTTP(loaded, get)
	if loaded.Code != http.StatusOK || !strings.Contains(loaded.Body.String(), `"type":"proxy"`) || !strings.Contains(loaded.Body.String(), `"endpoint":"https://upstream.example"`) {
		t.Fatalf("get proxy=%d body=%s", loaded.Code, loaded.Body.String())
	}

	// Proxy capabilities are read-only plus cache reclaim: no publish, delete,
	// restore, or retain.
	capabilitiesRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+id+"/capabilities", nil)
	authorize(capabilitiesRequest, "admin-secret")
	capabilities := httptest.NewRecorder()
	handler.ServeHTTP(capabilities, capabilitiesRequest)
	body := capabilities.Body.String()
	if capabilities.Code != http.StatusOK || !strings.Contains(body, `"type":"proxy"`) {
		t.Fatalf("proxy capabilities=%d %s", capabilities.Code, body)
	}
	for _, operation := range []string{`"read"`, `"browse"`, `"reclaim"`} {
		if !strings.Contains(body, operation) {
			t.Fatalf("proxy capabilities missing %s: %s", operation, body)
		}
	}
	for _, operation := range []string{`"publish"`, `"delete"`, `"restore"`, `"retain"`} {
		if strings.Contains(body, operation) {
			t.Fatalf("proxy capabilities must not contain %s: %s", operation, body)
		}
	}

	// Hosted remains the default when type is omitted.
	hosted := create(`{"name":"plain-hosted","format":"raw"}`, "create-plain-hosted")
	if hosted.Code != http.StatusCreated || !strings.Contains(hosted.Body.String(), `"type":"hosted"`) {
		t.Fatalf("default hosted=%d body=%s", hosted.Code, hosted.Body.String())
	}
	npmProxy := create(`{"name":"npm-proxy","format":"npm","type":"proxy","endpoint":"https://registry.npmjs.org","allowedHosts":["registry.npmjs.org"]}`, "create-npm-proxy")
	if npmProxy.Code != http.StatusCreated {
		t.Fatalf("create npm proxy=%d body=%s", npmProxy.Code, npmProxy.Body.String())
	}
}

func TestHostedRepositoryManagementRejectsInvalidProxyShapes(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	create := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("Idempotency-Key", "key-"+body)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	cases := map[string]string{
		"proxy without endpoint":           `{"name":"proxy-no-endpoint","format":"raw","type":"proxy"}`,
		"proxy with http endpoint":         `{"name":"proxy-http","format":"raw","type":"proxy","endpoint":"http://upstream.example","allowedHosts":["upstream.example"]}`,
		"proxy with malformed endpoint":    `{"name":"proxy-bad-url","format":"raw","type":"proxy","endpoint":"not a url","allowedHosts":["upstream.example"]}`,
		"raw proxy without allowedHosts":   `{"name":"proxy-no-hosts","format":"raw","type":"proxy","endpoint":"https://upstream.example"}`,
		"conan proxy without allowedHosts": `{"name":"proxy-conan-no-hosts","format":"conan","type":"proxy","endpoint":"https://upstream.example"}`,
		"npm proxy without allowedHosts":   `{"name":"proxy-npm-no-hosts","format":"npm","type":"proxy","endpoint":"https://registry.npmjs.org"}`,
		"hosted with endpoint":             `{"name":"hosted-endpoint","format":"raw","endpoint":"https://upstream.example"}`,
		"hosted with allowedHosts":         `{"name":"hosted-hosts","format":"raw","allowedHosts":["upstream.example"]}`,
		"unknown type":                     `{"name":"unknown-type","format":"raw","type":"virtual"}`,
	}
	for name, body := range cases {
		if response := create(body); response.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", name, response.Code, response.Body.String())
		}
	}
}

func TestCrossFormatArtifactSearchUsesFormatProjectionsAndBoundPagination(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	formats := []repository.Format{repository.FormatOCI, repository.FormatMaven, repository.FormatRaw, repository.FormatConan}
	repositories := make(map[repository.Format]repository.HostedRepository, len(formats))
	for _, format := range formats {
		repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "search-" + string(format), Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "search-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
			t.Fatal(err)
		}
		repositories[format] = repo
	}
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oci := repositories[repository.FormatOCI]
	for _, name := range []string{"team/alpha", "team/beta"} {
		if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: oci.ID, Name: name, Digest: digest, ObjectKey: "oci/" + name, MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1}, "latest"); err != nil {
			t.Fatal(err)
		}
	}
	maven := repositories[repository.FormatMaven]
	for i, coordinate := range []string{"org.example:alpha:1.0", "org.example:beta:1.0"} {
		id := uuid.NewString()
		objectName := fmt.Sprintf("artifact-%d.pom", i)
		objectKey := "maven/" + id
		if _, err := store.CreateMavenPublishSession(ctx, repository.MavenPublishSession{ID: id, RepositoryID: maven.ID, Coordinate: coordinate, Publisher: "search", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: objectName, Digest: digest, Size: 1}}}); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkMavenPublishObject(ctx, id, objectName, objectKey); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CommitMavenPublishSession(ctx, id, []repository.MavenAsset{{RepositoryID: maven.ID, Path: objectName, ObjectKey: objectKey, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	raw := repositories[repository.FormatRaw]
	for i, path := range []string{"releases/alpha.bin", "releases/beta.bin"} {
		if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: raw.ID, Path: path, Digest: digest, ObjectKey: fmt.Sprintf("raw/%d", i), Size: int64(1<<40) + int64(i), ContentType: "application/octet-stream"}); err != nil {
			t.Fatal(err)
		}
	}
	conan := repositories[repository.FormatConan]
	for i, reference := range []string{"pkg/1.0/user/stable", "pkg/2.0/user/stable"} {
		objectKey := fmt.Sprintf("conan/%d", i)
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: conan.ID, ObjectKey: objectKey, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: conan.ID, Reference: reference, Revision: fmt.Sprintf("rrev-%d", i), Digest: digest}, []repository.ConanAsset{{RepositoryID: conan.ID, Reference: reference, RecipeRevision: fmt.Sprintf("rrev-%d", i), Path: "conanfile.py", ObjectKey: objectKey, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(repo repository.HostedRepository, q string, token string) *httptest.ResponseRecorder {
		t.Helper()
		values := url.Values{"q": {q}, "pageSize": {"1"}}
		if token != "" {
			values.Set("pageToken", token)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-search?"+values.Encode(), nil)
		authorize(req, authenticator.IssueToken("search-reader"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	tests := []struct {
		format repository.Format
		query  string
		want   string
	}{
		{repository.FormatOCI, "team/", "team/alpha"},
		{repository.FormatMaven, "org.example:", "org.example:alpha:1.0"},
		{repository.FormatRaw, "releases/", "releases/alpha.bin"},
		{repository.FormatConan, "pkg/", "pkg/1.0/user/stable"},
	}
	var rawPage struct {
		Items []struct {
			Coordinate  string `json:"coordinate"`
			Size        int64  `json:"size"`
			ContentType string `json:"contentType"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	for _, test := range tests {
		response := request(repositories[test.format], test.query, "")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.want) {
			t.Fatalf("%s search=%d body=%s", test.format, response.Code, response.Body.String())
		}
		if test.format == repository.FormatRaw {
			if err := json.NewDecoder(response.Body).Decode(&rawPage); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(rawPage.Items) != 1 || rawPage.Items[0].Size != 1<<40 || rawPage.Items[0].ContentType != "application/octet-stream" || rawPage.NextPageToken == "" {
		t.Fatalf("raw page=%#v", rawPage)
	}
	next := request(raw, "releases/", rawPage.NextPageToken)
	if next.Code != http.StatusOK || !strings.Contains(next.Body.String(), "releases/beta.bin") {
		t.Fatalf("raw next=%d body=%s", next.Code, next.Body.String())
	}
	if changedQuery := request(raw, "other/", rawPage.NextPageToken); changedQuery.Code != http.StatusBadRequest || !strings.Contains(changedQuery.Body.String(), "invalid_page_token") {
		t.Fatalf("changed query=%d body=%s", changedQuery.Code, changedQuery.Body.String())
	}
	if wrongRepository := request(maven, "org.example:", rawPage.NextPageToken); wrongRepository.Code != http.StatusBadRequest || !strings.Contains(wrongRepository.Body.String(), "invalid_page_token") {
		t.Fatalf("wrong repository=%d body=%s", wrongRepository.Code, wrongRepository.Body.String())
	}
	denied := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+raw.ID+"/artifact-search", nil)
	authorize(denied, authenticator.IssueToken("ungranted"))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("denied=%d body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}
}

func TestMavenSnapshotSearchPaginationPreservesBuildPositionAcrossSurfaces(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "snapshot-search", Format: repository.FormatMaven, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "snapshot-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: uuid.NewString(), Name: "snapshot-search-group", Format: repository.FormatMaven, AnonymousRead: true,
		Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}},
	}, "test", "snapshot-search-group", "snapshot-search-group")
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)

	coordinate := "org.example:demo:1.0-SNAPSHOT"
	for build := 1; build <= 3; build++ {
		sessionID := uuid.NewString()
		objectName := fmt.Sprintf("demo-build-%d.pom", build)
		objectKey := "maven/snapshot-search/" + sessionID
		digest := "sha256:" + fmt.Sprintf("%064x", build)
		session := repository.MavenPublishSession{ID: sessionID, RepositoryID: repo.ID, Coordinate: coordinate, Publisher: "snapshot-publisher", State: "open", ExpiresAt: time.Now().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: objectName, Digest: digest, Size: 1}}}
		if _, err = store.CreateMavenPublishSession(ctx, session); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkMavenPublishObject(ctx, sessionID, objectName, objectKey); err != nil {
			t.Fatal(err)
		}
		if _, err = store.CommitMavenPublishSession(ctx, sessionID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: objectName, ObjectKey: objectKey, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	assertPages := func(name, path string, authenticated bool) {
		t.Helper()
		token := ""
		for wantBuild := 1; wantBuild <= 3; wantBuild++ {
			values := url.Values{"q": {"org.example:demo:"}, "pageSize": {"1"}}
			if strings.Contains(path, "/artifact-search") && !strings.Contains(path, "/repositories/") {
				values.Set("format", "maven")
			}
			if token != "" {
				values.Set("pageToken", token)
			}
			request := httptest.NewRequest(http.MethodGet, path+"?"+values.Encode(), nil)
			if authenticated {
				authorize(request, authenticator.IssueToken("snapshot-reader"))
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var page struct {
				Items []struct {
					Coordinate  string `json:"coordinate"`
					BuildNumber int    `json:"buildNumber"`
				} `json:"items"`
				NextPageToken string `json:"nextPageToken"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Coordinate != coordinate || page.Items[0].BuildNumber != wantBuild {
				t.Fatalf("%s build %d: status=%d body=%s", name, wantBuild, response.Code, response.Body.String())
			}
			token = page.NextPageToken
			if wantBuild < 3 && token == "" {
				t.Fatalf("%s build %d: missing next page token", name, wantBuild)
			}
		}
		if token != "" {
			t.Fatalf("%s final page has next token", name)
		}
	}

	assertPages("repository", "/api/v2/repositories/"+repo.ID+"/artifact-search", true)
	assertPages("anonymous group", "/api/v2/repositories/"+group.ID+"/artifact-search", false)
	assertPages("global", "/api/v2/artifact-search", true)
}

func TestOCIManifestBrowseIncludesUntaggedManifest(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "oci-untagged", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("d", 64)
	if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "nginx", Digest: digest, ObjectKey: "oci/untagged", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1989}, digest); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/oci/manifests?name=nginx&pageSize=50", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"digest":"`+digest+`"`) || !strings.Contains(response.Body.String(), `"tags":[]`) {
		t.Fatalf("untagged OCI manifest browse=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAnonymousHostedGroupArtifactSearchAggregatesOnlyAnonymousMembers(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	public, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "public-group-member", Format: repository.FormatOCI, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "private-group-member", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: public.ID, Name: "team/public", Digest: digest, ObjectKey: "public", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1}, "latest"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: private.ID, Name: "team/private", Digest: digest, ObjectKey: "private", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1}, "latest"); err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{
		ID: uuid.NewString(), Name: "public-search-group", Format: repository.FormatOCI, AnonymousRead: true,
		Members: []repository.GroupMember{{RepositoryID: public.ID, Position: 0}, {RepositoryID: private.ID, Position: 1}},
	}, "test", "public-search-group", "public-search-group")
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+group.ID+"/artifact-search?q=team%2F&pageSize=50", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"coordinate":"team/public"`) {
		t.Fatalf("anonymous group search=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "team/private") {
		t.Fatalf("private member leaked into group search: %s", response.Body.String())
	}
}

func TestConanRecipeRevisionSearchPaginatesAndBindsCursorToQuery(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-versions", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "conan-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	reference := "pkg/1.0/user/stable"
	for i, revision := range []string{"build-alpha", "build-beta", "build-gamma"} {
		digest := "sha256:" + strings.Repeat(string(rune('a'+i)), 64)
		key := "conan/versions/" + revision
		if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: repo.ID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(query, token string, pageSize int) *httptest.ResponseRecorder {
		t.Helper()
		values := url.Values{"reference": {reference}, "pageSize": {strconv.Itoa(pageSize)}}
		if query != "" {
			values.Set("q", query)
		}
		if token != "" {
			values.Set("pageToken", token)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/conan/recipe-revisions?"+values.Encode(), nil)
		authorize(req, authenticator.IssueToken("conan-reader"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	type revisionPage struct {
		Items []struct {
			Revision string `json:"revision"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	first := request("", "", 2)
	var firstPage revisionPage
	if first.Code != http.StatusOK || json.Unmarshal(first.Body.Bytes(), &firstPage) != nil || len(firstPage.Items) != 2 || firstPage.Items[0].Revision != "build-alpha" || firstPage.NextPageToken == "" {
		t.Fatalf("first page=%d body=%s", first.Code, first.Body.String())
	}
	second := request("", firstPage.NextPageToken, 2)
	var secondPage revisionPage
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &secondPage) != nil || len(secondPage.Items) != 1 || secondPage.Items[0].Revision != "build-gamma" || secondPage.NextPageToken != "" {
		t.Fatalf("second page=%d body=%s", second.Code, second.Body.String())
	}
	filtered := request("beta", "", 2)
	var filteredPage revisionPage
	if filtered.Code != http.StatusOK || json.Unmarshal(filtered.Body.Bytes(), &filteredPage) != nil || len(filteredPage.Items) != 1 || filteredPage.Items[0].Revision != "build-beta" {
		t.Fatalf("filtered=%d body=%s", filtered.Code, filtered.Body.String())
	}
	if invalid := request("beta", firstPage.NextPageToken, 2); invalid.Code != http.StatusBadRequest {
		t.Fatalf("query-bound cursor=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestAnonymousHostedGroupConanRecipeRevisionsAggregateMembers(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	public, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-public", Format: repository.FormatConan, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-private", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	reference := "demo/1.0/user/stable"
	publish := func(repoID, revision string) {
		digestChar := "a"
		if revision == "private-revision" {
			digestChar = "b"
		}
		digest := "sha256:" + strings.Repeat(digestChar, 64)
		key := "conan/group/" + repoID + "/" + revision
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repoID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: repoID, Reference: reference, Revision: revision, Digest: digest}, []repository.ConanAsset{{RepositoryID: repoID, Reference: reference, RecipeRevision: revision, Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	publish(public.ID, "public-revision")
	publish(private.ID, "private-revision")
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "conan-public-group", Format: repository.FormatConan, AnonymousRead: true, Members: []repository.GroupMember{{RepositoryID: public.ID, Position: 0}, {RepositoryID: private.ID, Position: 1}}}, "test", "conan-public-group", "conan-public-group")
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+group.ID+"/conan/recipe-revisions?reference="+url.QueryEscape(reference), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "public-revision") || strings.Contains(response.Body.String(), "private-revision") {
		t.Fatalf("anonymous group revisions=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateHostedGroupAcceptsConanFormat(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-group-member", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(fmt.Sprintf(`{"name":"conan-group","format":"conan","members":[{"repositoryId":"%s","position":0}]}`, repo.ID)))
	authorize(request, "admin-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-conan-group")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"format":"conan"`) {
		t.Fatalf("create Conan group=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCreateHostedGroupAcceptsNPMFormat(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "npm-group-member", Format: repository.FormatNPM})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(fmt.Sprintf(`{"name":"npm-group","format":"npm","members":[{"repositoryId":"%s","position":0}]}`, repo.ID)))
	authorize(request, "admin-secret")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "create-npm-group")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"format":"npm"`) {
		t.Fatalf("create npm group=%d body=%s", response.Code, response.Body.String())
	}
}

func TestV2AuditAPIExposesOptionalGrantDecisionFields(t *testing.T) {
	store := repository.NewMemoryStore()
	store.Audits = []repository.AuditRecord{{
		GroupName: "releases", Repository: "releases", Actor: "reader", Outcome: repository.AuditAccessDenied,
		OccurredAt: time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC), Format: "maven", Operation: "get", Status: http.StatusForbidden,
		AuthorizationSource: "repository_grants", AuthorizationReason: "scope_not_granted",
	}}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	request := httptest.NewRequest(http.MethodGet, "/api/v2/audits?group=releases&repository=releases&limit=1", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var audits []struct {
		AuthorizationSource string    `json:"authorizationSource"`
		AuthorizationReason string    `json:"authorizationReason"`
		OccurredAt          time.Time `json:"occurredAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&audits); err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].AuthorizationSource != "repository_grants" || audits[0].AuthorizationReason != "scope_not_granted" || audits[0].OccurredAt.IsZero() {
		t.Fatalf("audits=%#v", audits)
	}

	nonAdmin := httptest.NewRequest(http.MethodGet, "/api/v2/audits", nil)
	authorize(nonAdmin, "resolver-secret")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, nonAdmin)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("non-admin status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func TestHostedGroupManagementLifecycle(t *testing.T) {
	store := repository.NewMemoryStore()
	first, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-first", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-second", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-other", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"maven-group","format":"maven","members":[{"repositoryId":"`+second.ID+`","position":1},{"repositoryId":"`+first.ID+`","position":0}]}`))
	authorize(create, "admin-secret")
	create.Header.Set("Idempotency-Key", "group-create")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var group repository.HostedGroup
	if err := json.NewDecoder(created.Body).Decode(&group); err != nil || group.Version != "1" || len(group.Members) != 2 || group.Members[0].RepositoryID != first.ID {
		t.Fatalf("group=%#v err=%v", group, err)
	}
	get := httptest.NewRequest(http.MethodGet, "/api/v2/groups/"+group.ID, nil)
	authorize(get, "admin-secret")
	got := httptest.NewRecorder()
	handler.ServeHTTP(got, get)
	if got.Code != http.StatusOK {
		t.Fatalf("get=%d body=%s", got.Code, got.Body.String())
	}
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID+"/members", strings.NewReader(`[{"repositoryId":"`+second.ID+`","position":0}]`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), `"version":"2"`) {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID+"/members", strings.NewReader(`[{"repositoryId":"`+first.ID+`","position":0}]`))
	authorize(stale, "admin-secret")
	stale.Header.Set("If-Match", "1")
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	mismatch := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"invalid-group","format":"maven","members":[{"repositoryId":"`+other.ID+`","position":0}]}`))
	authorize(mismatch, "admin-secret")
	mismatch.Header.Set("Idempotency-Key", "invalid-group")
	mismatchResult := httptest.NewRecorder()
	handler.ServeHTTP(mismatchResult, mismatch)
	if mismatchResult.Code != http.StatusBadRequest {
		t.Fatalf("mismatch=%d body=%s", mismatchResult.Code, mismatchResult.Body.String())
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v2/groups/"+group.ID, nil)
	authorize(deleteRequest, "admin-secret")
	deleted := httptest.NewRecorder()
	handler.ServeHTTP(deleted, deleteRequest)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestRepositoryGrantManagementUsesETagVersioning(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "grant-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/grants", nil)
	authorize(list, "admin-secret")
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	if listed.Code != http.StatusOK || listed.Header().Get("ETag") != "1" || listed.Body.String() != "[]\n" {
		t.Fatalf("list=%d etag=%q body=%s", listed.Code, listed.Header().Get("ETag"), listed.Body.String())
	}
	replace := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read"],"resourcePrefix":"releases/"},{"principal":"build-agent","scopes":["repositories:write"],"resourcePrefix":"snapshots/"}]`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || replaced.Header().Get("ETag") != "2" || !strings.Contains(replaced.Body.String(), `"resourcePrefix":"releases/"`) {
		t.Fatalf("replace=%d etag=%q body=%s", replaced.Code, replaced.Header().Get("ETag"), replaced.Body.String())
	}
	stale := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[]`))
	authorize(stale, "admin-secret")
	stale.Header.Set("If-Match", "1")
	staleResult := httptest.NewRecorder()
	handler.ServeHTTP(staleResult, stale)
	if staleResult.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", staleResult.Code, staleResult.Body.String())
	}
	invalid := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["unknown"]}]`))
	authorize(invalid, "admin-secret")
	invalid.Header.Set("If-Match", "2")
	invalidResult := httptest.NewRecorder()
	handler.ServeHTTP(invalidResult, invalid)
	if invalidResult.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d body=%s", invalidResult.Code, invalidResult.Body.String())
	}
	duplicate := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read"],"resourcePrefix":"releases/"},{"principal":"build-agent","scopes":["repositories:write"],"resourcePrefix":"releases/"}]`))
	authorize(duplicate, "admin-secret")
	duplicate.Header.Set("If-Match", "2")
	duplicateResult := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResult, duplicate)
	if duplicateResult.Code != http.StatusBadRequest {
		t.Fatalf("duplicate=%d body=%s", duplicateResult.Code, duplicateResult.Body.String())
	}
	badPrefix := httptest.NewRequest(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", strings.NewReader(`[{"principal":"build-agent","scopes":["repositories:read"],"resourcePrefix":"/absolute"}]`))
	authorize(badPrefix, "admin-secret")
	badPrefix.Header.Set("If-Match", "2")
	badPrefixResult := httptest.NewRecorder()
	handler.ServeHTTP(badPrefixResult, badPrefix)
	if badPrefixResult.Code != http.StatusBadRequest {
		t.Fatalf("bad prefix=%d body=%s", badPrefixResult.Code, badPrefixResult.Body.String())
	}
}

func TestRepositoryConsoleAggregatesRequireAdminAndReturnCrossRepositoryData(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "aggregate-first", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "aggregate-second", Format: repository.FormatMaven})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, first.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "releases/"}}, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: first.ID, Path: "release.txt", Digest: "sha256:aggregate", Size: 17}); err != nil {
		t.Fatal(err)
	}
	job := repository.LifecycleJob{ID: uuid.NewString(), RepositoryID: second.ID, Kind: repository.LifecycleJobRetention, IdempotencyKey: "aggregate-retention"}
	if _, _, err = store.EnqueueLifecycleJob(ctx, job); err != nil {
		t.Fatal(err)
	}

	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			authorize(req, token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	if response := request("/api/v2/repository-grants", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous grants=%d body=%s", response.Code, response.Body.String())
	}

	grantsResponse := request("/api/v2/repository-grants", "admin-secret")
	var grants adminopenapi.RepositoryGrantRecordList
	if grantsResponse.Code != http.StatusOK || json.Unmarshal(grantsResponse.Body.Bytes(), &grants) != nil || len(grants) != 1 || grants[0].RepositoryId.String() != first.ID || grants[0].Principal != "build-agent" {
		t.Fatalf("grants=%d body=%s decoded=%#v", grantsResponse.Code, grantsResponse.Body.String(), grants)
	}

	capacitiesResponse := request("/api/v2/repository-capacities", "admin-secret")
	var capacities adminopenapi.RepositoryCapacityList
	if capacitiesResponse.Code != http.StatusOK || json.Unmarshal(capacitiesResponse.Body.Bytes(), &capacities) != nil || len(capacities) != 2 {
		t.Fatalf("capacities=%d body=%s decoded=%#v", capacitiesResponse.Code, capacitiesResponse.Body.String(), capacities)
	}
	capacityByID := make(map[string]adminopenapi.RepositoryCapacity, len(capacities))
	for _, capacity := range capacities {
		capacityByID[capacity.RepositoryId.String()] = capacity
	}
	if capacityByID[first.ID].UsedBytes != 17 || capacityByID[first.ID].ObjectCount != 1 {
		t.Fatalf("raw capacity=%#v", capacityByID[first.ID])
	}

	jobsResponse := request("/api/v2/lifecycle-jobs?limit=10", "admin-secret")
	var jobs adminopenapi.RepositoryLifecycleJobList
	if jobsResponse.Code != http.StatusOK || json.Unmarshal(jobsResponse.Body.Bytes(), &jobs) != nil || len(jobs) != 1 || jobs[0].RepositoryId.String() != second.ID || jobs[0].RepositoryName != second.Name || jobs[0].Job.Id != job.ID {
		t.Fatalf("jobs=%d body=%s decoded=%#v", jobsResponse.Code, jobsResponse.Body.String(), jobs)
	}
}

func TestRepositoryManagementUsesScopedGrants(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "scoped-target", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{
		{Principal: "reader", Scopes: []string{"repositories:read"}},
		{Principal: "writer", Scopes: []string{"repositories:write"}},
		{Principal: "manager", Scopes: []string{"repositories:admin"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(method, path, actor, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		authorize(r, authenticator.IssueToken(actor))
		if method == http.MethodPut {
			r.Header.Set("If-Match", "2")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID, "reader", ""); response.Code != http.StatusOK {
		t.Fatalf("reader get=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/retention-policy", "reader", ""); response.Code != http.StatusOK {
		t.Fatalf("reader policy=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "/api/v2/repositories/"+repo.ID+"/grants", "reader", `[]`); response.Code != http.StatusForbidden {
		t.Fatalf("reader grants=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/grants", "manager", ""); response.Code != http.StatusOK {
		t.Fatalf("manager grants=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodDelete, "/api/v2/repositories/"+repo.ID, "reader", ""); response.Code != http.StatusForbidden {
		t.Fatalf("reader delete=%d body=%s", response.Code, response.Body.String())
	}
	if len(store.Audits) == 0 {
		t.Fatal("expected authorization audit")
	}
	audit := store.Audits[len(store.Audits)-1]
	if audit.Outcome != repository.AuditAccessDenied || audit.AuthorizationSource != "repository_grants" || audit.AuthorizationReason != "scope_not_granted" || audit.Format != "management" {
		t.Fatalf("audit=%#v", audit)
	}
	metrics := httptest.NewRecorder()
	handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `artifact_gateway_repository_authorization_denials_total{format="management",authorization_source="repository_grants",authorization_reason="scope_not_granted"} 2`) {
		t.Fatalf("management authorization metric=%s", metrics.Body.String())
	}
	if response := request(http.MethodDelete, "/api/v2/repositories/"+repo.ID, "writer", ""); response.Code != http.StatusAccepted {
		t.Fatalf("writer delete=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHostedRepositoryManagementRejectsAnonymousAndInvalidRequests(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v2/repositories", nil))
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status=%d", denied.Code)
	}
	anonymousInvalidSession := httptest.NewRecorder()
	handler.ServeHTTP(anonymousInvalidSession, httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/not-a-uuid:commit", nil))
	if anonymousInvalidSession.Code != http.StatusUnauthorized || !strings.Contains(anonymousInvalidSession.Body.String(), `"code":"access_denied"`) {
		t.Fatalf("anonymous invalid session=%d body=%s", anonymousInvalidSession.Code, anonymousInvalidSession.Body.String())
	}
	bad := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"Bad Name","format":"npm"}`))
	authorize(bad, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, bad)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", response.Code, response.Body.String())
	}
	page := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageToken=unknown", nil)
	authorize(page, "admin-secret")
	paged := httptest.NewRecorder()
	handler.ServeHTTP(paged, page)
	if paged.Code != http.StatusBadRequest || !strings.Contains(paged.Body.String(), `"code":"invalid_page_token"`) {
		t.Fatalf("invalid page token=%d body=%s", paged.Code, paged.Body.String())
	}
	invalidID := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/not-a-uuid", nil)
	authorize(invalidID, "admin-secret")
	invalidIDResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidIDResponse, invalidID)
	if invalidIDResponse.Code != http.StatusBadRequest || !strings.Contains(invalidIDResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid id=%d body=%s", invalidIDResponse.Code, invalidIDResponse.Body.String())
	}
	invalidArtifactRepositoryID := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/not-a-uuid/artifacts", nil)
	authorize(invalidArtifactRepositoryID, "admin-secret")
	invalidArtifactRepositoryIDResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidArtifactRepositoryIDResponse, invalidArtifactRepositoryID)
	if invalidArtifactRepositoryIDResponse.Code != http.StatusBadRequest || !strings.Contains(invalidArtifactRepositoryIDResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid artifact repository id=%d body=%s", invalidArtifactRepositoryIDResponse.Code, invalidArtifactRepositoryIDResponse.Body.String())
	}
	invalidSessionID := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/not-a-uuid:commit", nil)
	authorize(invalidSessionID, "admin-secret")
	invalidSessionIDResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidSessionIDResponse, invalidSessionID)
	if invalidSessionIDResponse.Code != http.StatusBadRequest || !strings.Contains(invalidSessionIDResponse.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("invalid session id=%d body=%s", invalidSessionIDResponse.Code, invalidSessionIDResponse.Body.String())
	}
	nonCommitSessionPost := httptest.NewRequest(http.MethodPost, "/api/v2/publish-sessions/"+uuid.NewString(), nil)
	authorize(nonCommitSessionPost, "admin-secret")
	nonCommitSessionPostResponse := httptest.NewRecorder()
	handler.ServeHTTP(nonCommitSessionPostResponse, nonCommitSessionPost)
	if nonCommitSessionPostResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("non-commit session post=%d body=%s", nonCommitSessionPostResponse.Code, nonCommitSessionPostResponse.Body.String())
	}
}

func TestHostedRepositoryIdempotencyAndPagination(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	create := func(name, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(`{"name":"`+name+`","format":"raw"}`))
		authorize(r, "admin-secret")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if missing := create("missing", ""); missing.Code != http.StatusBadRequest {
		t.Fatalf("missing key=%d", missing.Code)
	}
	first := create("one", "same-key")
	if first.Code != http.StatusCreated {
		t.Fatalf("first=%d %s", first.Code, first.Body.String())
	}
	replay := create("one", "same-key")
	if replay.Code != http.StatusCreated || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay=%d %s", replay.Code, replay.Body.String())
	}
	if conflict := create("two", "same-key"); conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "idempotency_conflict") {
		t.Fatalf("conflict=%d %s", conflict.Code, conflict.Body.String())
	}
	if second := create("two", "two-key"); second.Code != http.StatusCreated {
		t.Fatalf("second=%d", second.Code)
	}
	list := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageSize=1", nil)
	authorize(list, "admin-secret")
	pageOne := httptest.NewRecorder()
	handler.ServeHTTP(pageOne, list)
	if pageOne.Code != http.StatusOK {
		t.Fatalf("page one=%d", pageOne.Code)
	}
	var decoded repositoryPage
	if err := json.NewDecoder(pageOne.Body).Decode(&decoded); err != nil || len(decoded.Items) != 1 || decoded.NextPageToken == "" {
		t.Fatalf("page=%#v err=%v", decoded, err)
	}
	pageTwo := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageSize=1&pageToken="+decoded.NextPageToken, nil)
	authorize(pageTwo, "admin-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, pageTwo)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), decoded.Items[0].ID) {
		t.Fatalf("page two=%d %s", w.Code, w.Body.String())
	}
	payload, _ := json.Marshal(repositoryPageCursor{Endpoint: "repositories", ID: decoded.Items[0].ID, ExpiresAt: time.Now().Add(-time.Second).Unix()})
	mac := hmac.New(sha256.New, []byte("admin-secret"))
	_, _ = mac.Write(payload)
	expired := base64.RawURLEncoding.EncodeToString(append(payload, mac.Sum(nil)...))
	expiredRequest := httptest.NewRequest(http.MethodGet, "/api/v2/repositories?pageToken="+expired, nil)
	authorize(expiredRequest, "admin-secret")
	expiredResponse := httptest.NewRecorder()
	handler.ServeHTTP(expiredResponse, expiredRequest)
	if expiredResponse.Code != http.StatusBadRequest || !strings.Contains(expiredResponse.Body.String(), "invalid_page_token") {
		t.Fatalf("expired token=%d %s", expiredResponse.Code, expiredResponse.Body.String())
	}
}

func TestNativeRepositoryGuardAllowsAnonymousReadPolicyAndDeniesDisabledProtocols(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	for _, format := range []repository.Format{repository.FormatRaw, repository.FormatOCI, repository.FormatMaven} {
		repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: string(format) + "-native", Format: format, AnonymousRead: true})
		if err != nil {
			t.Fatal(err)
		}
		path := map[repository.Format]string{repository.FormatRaw: "/raw/raw-native/a", repository.FormatOCI: "/v2/oci-native/app/manifests/latest", repository.FormatMaven: "/maven/maven-native/a.pom"}[format]
		anonymous := httptest.NewRecorder()
		handler.ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
		if anonymous.Code == http.StatusUnauthorized {
			t.Fatalf("%s anonymous=%d", format, anonymous.Code)
		}
		if _, err := store.DisableHostedRepository(context.Background(), repo.ID); err != nil {
			t.Fatal(err)
		}
		disabled := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		authorize(r, "resolver-secret")
		handler.ServeHTTP(disabled, r)
		if disabled.Code != http.StatusForbidden {
			t.Fatalf("%s disabled=%d", format, disabled.Code)
		}
	}
}

func TestMemoryHostedRepositoryHonorsPageSize200(t *testing.T) {
	store := repository.NewMemoryStore()
	for i := 0; i < 201; i++ {
		if _, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: fmt.Sprintf("repo-%03d", i), Format: repository.FormatRaw}); err != nil {
			t.Fatal(err)
		}
	}
	items, next, err := store.ListHostedRepositories(context.Background(), 200, "")
	if err != nil || len(items) != 200 || next == "" {
		t.Fatalf("items=%d next=%q err=%v", len(items), next, err)
	}
}

func TestRepositoryManagementUpdatesProxyConfiguration(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID:           uuid.NewString(),
		Name:         "raw-proxy",
		Format:       repository.FormatRaw,
		Type:         repository.RepositoryTypeProxy,
		Endpoint:     "https://upstream.example",
		AllowedHosts: []string{"upstream.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	patch := func(version, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, strings.NewReader(body))
		authorize(r, "admin-secret")
		r.Header.Set("If-Match", version)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec
	}

	updated := patch("1", `{"endpoint":"https://cdn.example","allowedHosts":["cdn.example"]}`)
	if updated.Code != http.StatusOK {
		t.Fatalf("update=%d body=%s", updated.Code, updated.Body.String())
	}
	if etag := updated.Header().Get("ETag"); etag != "2" {
		t.Fatalf("etag=%q want 2", etag)
	}
	if !strings.Contains(updated.Body.String(), `"endpoint":"https://cdn.example"`) {
		t.Fatalf("body=%s", updated.Body.String())
	}
	persisted, _ := store.GetHostedRepository(context.Background(), repo.ID)
	if persisted.Endpoint != "https://cdn.example" || persisted.AllowedHosts[0] != "cdn.example" {
		t.Fatalf("persisted endpoint=%q hosts=%v", persisted.Endpoint, persisted.AllowedHosts)
	}

	if stale := patch("1", `{"endpoint":"https://other.example","allowedHosts":["other.example"]}`); stale.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale=%d body=%s", stale.Code, stale.Body.String())
	}
	if invalid := patch("2", `{"endpoint":"not-a-url","allowedHosts":["x"]}`); invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid=%d body=%s", invalid.Code, invalid.Body.String())
	}
	if missingHosts := patch("2", `{"endpoint":"https://cdn.example","allowedHosts":[]}`); missingHosts.Code != http.StatusBadRequest {
		t.Fatalf("missing hosts=%d body=%s", missingHosts.Code, missingHosts.Body.String())
	}

	hosted, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "raw-hosted", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	hostedPatch := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+hosted.ID, strings.NewReader(`{"endpoint":"https://x.example"}`))
	authorize(hostedPatch, "admin-secret")
	hostedPatch.Header.Set("If-Match", "1")
	hostedRec := httptest.NewRecorder()
	handler.ServeHTTP(hostedRec, hostedPatch)
	if hostedRec.Code != http.StatusBadRequest {
		t.Fatalf("hosted update=%d body=%s", hostedRec.Code, hostedRec.Body.String())
	}
}

func TestRepositoryManagementAnonymousReadPolicyDefaultsAndUpdates(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	create := func(body, key string) repository.HostedRepository {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v2/repositories", strings.NewReader(body))
		authorize(req, "admin-secret")
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create=%d body=%s", rec.Code, rec.Body.String())
		}
		var repo repository.HostedRepository
		if err := json.NewDecoder(rec.Body).Decode(&repo); err != nil {
			t.Fatal(err)
		}
		return repo
	}

	private := create(`{"name":"private-hosted","format":"raw"}`, "private-hosted")
	if private.AnonymousRead {
		t.Fatalf("anonymousRead defaulted to true: %#v", private)
	}
	public := create(`{"name":"public-hosted","format":"raw","anonymousRead":true}`, "public-hosted")
	if !public.AnonymousRead {
		t.Fatalf("anonymousRead was not returned on create: %#v", public)
	}

	patch := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+private.ID, strings.NewReader(`{"anonymousRead":true}`))
	authorize(patch, "admin-secret")
	patch.Header.Set("If-Match", "1")
	patched := httptest.NewRecorder()
	handler.ServeHTTP(patched, patch)
	if patched.Code != http.StatusOK || !strings.Contains(patched.Body.String(), `"anonymousRead":true`) || patched.Header().Get("ETag") != "2" {
		t.Fatalf("patch=%d etag=%q body=%s", patched.Code, patched.Header().Get("ETag"), patched.Body.String())
	}
	stored, err := store.GetHostedRepository(context.Background(), private.ID)
	if err != nil || !stored.AnonymousRead {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
}

func TestRepositoryEffectiveAccessReportsPermissionsAndAnonymousPolicy(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "effective-raw", Format: repository.FormatRaw, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access", nil)
	authorize(request, authenticator.IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("effective access = %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Actor    string `json:"actor"`
		Identity struct {
			Kind string `json:"kind"`
		} `json:"identity"`
		AnonymousRead struct {
			Allowed bool   `json:"allowed"`
			Reason  string `json:"reason"`
		} `json:"anonymousRead"`
		Permissions struct {
			Read  struct{ Allowed bool } `json:"read"`
			Write struct{ Allowed bool } `json:"write"`
			Admin struct{ Allowed bool } `json:"admin"`
		} `json:"permissions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Actor != "reader" || body.Identity.Kind != "static_resolver" || !body.AnonymousRead.Allowed || body.AnonymousRead.Reason != "repository_anonymous_read_enabled" || !body.Permissions.Read.Allowed || body.Permissions.Write.Allowed || body.Permissions.Admin.Allowed {
		t.Fatalf("effective access body=%#v", body)
	}

	denied := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access", nil)
	authorize(denied, authenticator.IssueToken("stranger"))
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusOK || !strings.Contains(deniedResponse.Body.String(), `"actor":"stranger"`) || !strings.Contains(deniedResponse.Body.String(), `"read":{"allowed":false`) {
		t.Fatalf("denied effective access = %d %s", deniedResponse.Code, deniedResponse.Body.String())
	}

	if _, err := store.DisableHostedRepository(context.Background(), repo.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinalizeHostedRepositoryDeletion(context.Background(), repo.ID); err != nil {
		t.Fatal(err)
	}
	deleted := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access", nil)
	authorize(deleted, authenticator.IssueToken("reader"))
	deletedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deletedResponse, deleted)
	if deletedResponse.Code != http.StatusOK || !strings.Contains(deletedResponse.Body.String(), `"anonymousRead":{"allowed":false,"reason":"repository_not_active"`) {
		t.Fatalf("deleted effective access = %d %s", deletedResponse.Code, deletedResponse.Body.String())
	}
}

func TestRepositoryEffectiveAccessSupportsAdministratorSimulation(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "simulation-raw", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{
		Principal: "build-agent", Scopes: []string{"repositories:read"}, ResourcePrefix: "releases/",
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=build-agent&resource=releases%2Fapp.bin", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"simulated":true`) || !strings.Contains(response.Body.String(), `"resource":"releases/app.bin"`) || !strings.Contains(response.Body.String(), `"read":{"allowed":true,"reason":"scope_granted","source":"repository_grants"}`) {
		t.Fatalf("simulated grant = %d %s", response.Code, response.Body.String())
	}

	wrongResource := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=build-agent&resource=snapshots%2Fapp.bin", nil)
	authorize(wrongResource, "admin-secret")
	wrongResourceResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResourceResponse, wrongResource)
	if wrongResourceResponse.Code != http.StatusOK || !strings.Contains(wrongResourceResponse.Body.String(), `"read":{"allowed":false`) {
		t.Fatalf("wrong resource = %d %s", wrongResourceResponse.Code, wrongResourceResponse.Body.String())
	}

	globalRole := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=release-bot&role=writer", nil)
	authorize(globalRole, "admin-secret")
	globalRoleResponse := httptest.NewRecorder()
	handler.ServeHTTP(globalRoleResponse, globalRole)
	if globalRoleResponse.Code != http.StatusOK || !strings.Contains(globalRoleResponse.Body.String(), `"read":{"allowed":true,"reason":"role_writer","source":"role"}`) || !strings.Contains(globalRoleResponse.Body.String(), `"write":{"allowed":true,"reason":"role_writer","source":"role"}`) || !strings.Contains(globalRoleResponse.Body.String(), `"admin":{"allowed":false`) {
		t.Fatalf("simulated role = %d %s", globalRoleResponse.Code, globalRoleResponse.Body.String())
	}

	forbidden := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?actor=build-agent", nil)
	authorize(forbidden, authenticator.IssueToken("reader"))
	forbiddenResponse := httptest.NewRecorder()
	handler.ServeHTTP(forbiddenResponse, forbidden)
	if forbiddenResponse.Code != http.StatusForbidden {
		t.Fatalf("non-admin simulation = %d %s", forbiddenResponse.Code, forbiddenResponse.Body.String())
	}

	invalid := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/effective-access?role=reader", nil)
	authorize(invalid, "admin-secret")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("role without actor = %d %s", invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestCurrentIdentityReportsSafeCredentialMetadata(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	request := httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"actor":"alice"`) || !strings.Contains(response.Body.String(), `"kind":"static_admin"`) || !strings.Contains(response.Body.String(), `"role":"admin"`) || !strings.Contains(response.Body.String(), `"administrator":true`) {
		t.Fatalf("identity = %d %s", response.Code, response.Body.String())
	}

	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated identity = %d %s", unauthenticated.Code, unauthenticated.Body.String())
	}
}

func TestAnonymousRepositoryBrowseAllowsReadOnlyQueries(t *testing.T) {
	store := repository.NewMemoryStore()
	enableAnonymousAccess(t, store)
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "public-oci", Format: repository.FormatOCI, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{NativeOCIObjectStore: NewMemoryOCIObjectStore()}, store, TestAdapter{}, testAuthenticator())
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}`)
	put := httptest.NewRequest(http.MethodPut, "/v2/public-oci/app/manifests/latest", strings.NewReader(string(manifest)))
	put.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	authorize(put, "resolver-secret")
	published := httptest.NewRecorder()
	handler.ServeHTTP(published, put)
	if published.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}

	browse := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/oci/images", nil)
	browseResponse := httptest.NewRecorder()
	handler.ServeHTTP(browseResponse, browse)
	if browseResponse.Code != http.StatusOK || !strings.Contains(browseResponse.Body.String(), `"app"`) {
		t.Fatalf("anonymous browse = %d %s", browseResponse.Code, browseResponse.Body.String())
	}

	private, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "private-oci", Format: repository.FormatOCI})
	if err != nil {
		t.Fatal(err)
	}
	privateBrowse := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+private.ID+"/oci/images", nil)
	privateResponse := httptest.NewRecorder()
	handler.ServeHTTP(privateResponse, privateBrowse)
	if privateResponse.Code != http.StatusUnauthorized {
		t.Fatalf("private browse = %d %s", privateResponse.Code, privateResponse.Body.String())
	}
}

func TestGroupManagementAnonymousReadPolicyDefaultsAndUpdates(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "group-policy-repo", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	create := httptest.NewRequest(http.MethodPost, "/api/v2/groups", strings.NewReader(`{"name":"group-policy","format":"raw","members":[{"repositoryId":"`+repo.ID+`","position":0}]}`))
	authorize(create, "admin-secret")
	create.Header.Set("Idempotency-Key", "group-policy")
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create=%d body=%s", created.Code, created.Body.String())
	}
	var group repository.HostedGroup
	if err := json.NewDecoder(created.Body).Decode(&group); err != nil || group.AnonymousRead {
		t.Fatalf("group=%#v err=%v", group, err)
	}

	replace := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID, strings.NewReader(`{"name":"group-policy","format":"raw","anonymousRead":true,"members":[{"repositoryId":"`+repo.ID+`","position":0}]}`))
	authorize(replace, "admin-secret")
	replace.Header.Set("If-Match", "1")
	replaced := httptest.NewRecorder()
	handler.ServeHTTP(replaced, replace)
	if replaced.Code != http.StatusOK || !strings.Contains(replaced.Body.String(), `"anonymousRead":true`) {
		t.Fatalf("replace=%d body=%s", replaced.Code, replaced.Body.String())
	}

	membersOnly := httptest.NewRequest(http.MethodPut, "/api/v2/groups/"+group.ID+"/members", strings.NewReader(`[{"repositoryId":"`+repo.ID+`","position":0}]`))
	authorize(membersOnly, "admin-secret")
	membersOnly.Header.Set("If-Match", "2")
	membersOnlyResponse := httptest.NewRecorder()
	handler.ServeHTTP(membersOnlyResponse, membersOnly)
	if membersOnlyResponse.Code != http.StatusOK || !strings.Contains(membersOnlyResponse.Body.String(), `"anonymousRead":true`) {
		t.Fatalf("members replace=%d body=%s", membersOnlyResponse.Code, membersOnlyResponse.Body.String())
	}
}

func TestAPIKeyRolesEnforceScopedManagementAccess(t *testing.T) {
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{
		ID:           uuid.NewString(),
		Name:         "ci-proxy",
		Format:       repository.FormatRaw,
		Type:         repository.RepositoryTypeProxy,
		Endpoint:     "https://upstream.example",
		AllowedHosts: []string{"upstream.example"},
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	authenticator.APIKeys = store
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)

	createKey := func(t *testing.T, roles string) string {
		t.Helper()
		body := `{"name":"` + roles + `","roles":["` + roles + `"]}"`
		req := httptest.NewRequest(http.MethodPost, "/api/v2/api-keys", strings.NewReader(body))
		authorize(req, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s key=%d body=%s", roles, rec.Code, rec.Body.String())
		}
		var created struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Token == "" {
			t.Fatalf("parse %s key token: %s", roles, rec.Body.String())
		}
		return created.Token
	}

	readerToken := createKey(t, "reader")
	writerToken := createKey(t, "writer")

	patch := func(token string) int {
		req := httptest.NewRequest(http.MethodPatch, "/api/v2/repositories/"+repo.ID, strings.NewReader(`{"endpoint":"https://cdn.example","allowedHosts":["cdn.example"]}`))
		authorize(req, token)
		req.Header.Set("If-Match", "1")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	get := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID, nil)
		authorize(req, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	// Reader: read allowed by role, write denied.
	if code := get(readerToken); code != http.StatusOK {
		t.Fatalf("reader get=%d", code)
	}
	if code := patch(readerToken); code != http.StatusForbidden {
		t.Fatalf("reader patch=%d want 403", code)
	}

	// Writer: read and write allowed by role.
	if code := get(writerToken); code != http.StatusOK {
		t.Fatalf("writer get=%d", code)
	}
	if code := patch(writerToken); code != http.StatusOK {
		t.Fatalf("writer patch=%d want 200", code)
	}

	// Neither reader nor writer may mint new keys (administrator-only).
	for _, token := range []string{readerToken, writerToken} {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/api-keys", strings.NewReader(`{"name":"x","roles":["admin"]}`))
		authorize(req, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s role minted key=%d want 401", token, rec.Code)
		}
	}
}

func TestRepositoryManagementCancelsReplicationPlan(t *testing.T) {
	store := repository.NewMemoryStore()
	source, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "repl-src", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "repl-tgt", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := store.CreateReplicationPlan(context.Background(), repository.ReplicationPlan{
		ID: uuid.NewString(), SourceRepositoryID: source.ID, TargetRepositoryID: target.ID, Format: repository.FormatRaw, IdempotencyKey: "cancel-key",
	}, []repository.ReplicationCheckpoint{{PlanID: uuid.NewString(), SourceObjectKey: "a", ObjectKey: "a", Digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	cancel := func(repoID, planID string) int {
		req := httptest.NewRequest(http.MethodDelete, "/api/v2/repositories/"+repoID+"/replications/"+planID, nil)
		authorize(req, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := cancel(source.ID, plan.ID); code != http.StatusNoContent {
		t.Fatalf("cancel pending=%d want 204", code)
	}
	persisted, _ := store.GetReplicationPlan(context.Background(), source.ID, plan.ID)
	if persisted.State != "cancelled" {
		t.Fatalf("state=%q want cancelled", persisted.State)
	}
	// Already cancelled is not cancellable.
	if code := cancel(source.ID, plan.ID); code != http.StatusConflict {
		t.Fatalf("cancel cancelled=%d want 409", code)
	}
	// Unknown plan id is not found.
	if code := cancel(source.ID, uuid.NewString()); code != http.StatusNotFound {
		t.Fatalf("cancel missing=%d want 404", code)
	}
	// Plan scoped to a different repository is not found through this path.
	other, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: uuid.NewString(), Name: "repl-other", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if code := cancel(other.ID, plan.ID); code != http.StatusNotFound {
		t.Fatalf("cancel wrong repo=%d want 404", code)
	}
}

func TestUserManagementLoginAndSessionAuth(t *testing.T) {
	store := repository.NewMemoryStore()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())

	createUser := func(body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/users", strings.NewReader(body))
		authorize(req, "admin-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := createUser(`{"name":"alice","password":"supersecret","role":"reader"}`); code != http.StatusCreated {
		t.Fatalf("create alice=%d", code)
	}
	if code := createUser(`{"name":"alice","password":"supersecret","role":"reader"}`); code != http.StatusConflict {
		t.Fatalf("duplicate alice=%d want 409", code)
	}
	if code := createUser(`{"name":"bob","password":"short","role":"admin"}`); code != http.StatusBadRequest {
		t.Fatalf("short password=%d want 400", code)
	}
	if code := createUser(`{"name":"root","password":"supersecret","role":"admin"}`); code != http.StatusCreated {
		t.Fatalf("create root=%d", code)
	}
	if code := createUser(`{"name":"backup-admin","password":"supersecret","role":"admin"}`); code != http.StatusCreated {
		t.Fatalf("create backup admin=%d", code)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/v2/users", nil)
	authorize(list, "admin-secret")
	listRec := httptest.NewRecorder()
	handler.ServeHTTP(listRec, list)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"name":"root"`) {
		t.Fatalf("list users=%d body=%s", listRec.Code, listRec.Body.String())
	}

	login := func(body string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var resp struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		return rec.Code, resp.Token
	}
	if code, _ := login(`{"username":"root","password":"wrong"}`); code != http.StatusUnauthorized {
		t.Fatalf("wrong password login=%d want 401", code)
	}
	code, token := login(`{"username":"root","password":"supersecret"}`)
	if code != http.StatusOK || token == "" {
		t.Fatalf("root login=%d token=%q", code, token)
	}

	// The admin session token can call an admin-only endpoint; a reader cannot.
	asSession := func(target string) int {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		authorize(req, token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := asSession("/api/v2/users"); code != http.StatusOK {
		t.Fatalf("admin session list users=%d want 200", code)
	}
	identityRequest := httptest.NewRequest(http.MethodGet, "/api/v2/identity", nil)
	authorize(identityRequest, token)
	identityResponse := httptest.NewRecorder()
	handler.ServeHTTP(identityResponse, identityRequest)
	if identityResponse.Code != http.StatusOK || !strings.Contains(identityResponse.Body.String(), `"kind":"local_session"`) || !strings.Contains(identityResponse.Body.String(), `"role":"admin"`) {
		t.Fatalf("admin session identity=%d body=%s", identityResponse.Code, identityResponse.Body.String())
	}

	_, readerToken := login(`{"username":"alice","password":"supersecret"}`)
	readerReq := httptest.NewRequest(http.MethodGet, "/api/v2/users", nil)
	authorize(readerReq, readerToken)
	readerRec := httptest.NewRecorder()
	handler.ServeHTTP(readerRec, readerReq)
	if readerRec.Code != http.StatusUnauthorized {
		t.Fatalf("reader session list users=%d want 401", readerRec.Code)
	}

	// Disabling a user blocks both new logins and the existing session.
	root, _ := store.GetUserByName(context.Background(), "root")
	patch := httptest.NewRequest(http.MethodPatch, "/api/v2/users/"+root.ID, strings.NewReader(`{"state":"disabled"}`))
	authorize(patch, "admin-secret")
	patch.Header.Set("If-Match", root.Version)
	patchRec := httptest.NewRecorder()
	handler.ServeHTTP(patchRec, patch)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("disable root=%d body=%s", patchRec.Code, patchRec.Body.String())
	}
	if code, _ := login(`{"username":"root","password":"supersecret"}`); code != http.StatusUnauthorized {
		t.Fatalf("disabled login=%d want 401", code)
	}
	if code := asSession("/api/v2/users"); code != http.StatusUnauthorized {
		t.Fatalf("disabled session=%d want 401", code)
	}

	del := httptest.NewRequest(http.MethodDelete, "/api/v2/users/"+root.ID, nil)
	authorize(del, "admin-secret")
	delRec := httptest.NewRecorder()
	handler.ServeHTTP(delRec, del)
	if delRec.Code != http.StatusNoContent {
		t.Fatalf("delete root=%d want 204", delRec.Code)
	}
}
