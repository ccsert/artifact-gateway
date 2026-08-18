package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

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
	createdAt := time.Date(2026, 8, 13, 11, 14, 10, 0, time.UTC)
	if _, err = store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: repo.ID, Name: "nginx", Digest: digest, ObjectKey: "oci/untagged", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: 1989, CreatedAt: createdAt}, digest); err != nil {
		t.Fatal(err)
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, testAuthenticator())
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/oci/manifests?name=nginx&pageSize=50", nil)
	authorize(request, "admin-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"digest":"`+digest+`"`) || !strings.Contains(response.Body.String(), `"tags":[]`) || !strings.Contains(response.Body.String(), `"createdAt":"2026-08-13T11:14:10Z"`) {
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
