package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestGlobalArtifactSearchPaginatesReadableRepositoriesWithoutLeakingDeniedOnes(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	createRepository := func(id, name, path, digestCharacter, grantedActor string) repository.HostedRepository {
		t.Helper()
		repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: id, Name: name, Format: repository.FormatRaw})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: repo.ID, Path: path, Digest: "sha256:" + strings.Repeat(digestCharacter, 64), ObjectKey: "raw/" + name, Size: 7, ContentType: "application/octet-stream"}); err != nil {
			t.Fatal(err)
		}
		grants := []repository.RepositoryGrant{{Principal: grantedActor, Scopes: []string{"repositories:read"}}}
		if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, grants, "1"); err != nil {
			t.Fatal(err)
		}
		return repo
	}
	first := createRepository("00000000-0000-0000-0000-000000000003", "first", "packages/alpha.bin", "a", "search-user")
	denied := createRepository("00000000-0000-0000-0000-000000000002", "denied", "packages/private.bin", "b", "other-user")
	last := createRepository("00000000-0000-0000-0000-000000000001", "last", "packages/omega.bin", "c", "search-user")

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := func(query, pageToken string) *httptest.ResponseRecorder {
		t.Helper()
		values := url.Values{"q": {query}, "format": {"raw"}, "pageSize": {"1"}}
		if pageToken != "" {
			values.Set("pageToken", pageToken)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v2/artifact-search?"+values.Encode(), nil)
		authorize(req, authenticator.IssueToken("search-user"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	type searchPage struct {
		Items []struct {
			RepositoryID   string `json:"repositoryId"`
			RepositoryName string `json:"repositoryName"`
			Coordinate     string `json:"coordinate"`
		} `json:"items"`
		SearchedRepositories int    `json:"searchedRepositories"`
		NextPageToken        string `json:"nextPageToken"`
	}

	firstResponse := request("packages/", "")
	var firstPage searchPage
	if firstResponse.Code != http.StatusOK || json.Unmarshal(firstResponse.Body.Bytes(), &firstPage) != nil {
		t.Fatalf("first page=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	if len(firstPage.Items) != 1 || firstPage.Items[0].RepositoryID != first.ID || firstPage.Items[0].Coordinate != "packages/alpha.bin" || firstPage.SearchedRepositories != 2 || firstPage.NextPageToken == "" {
		t.Fatalf("first page=%#v", firstPage)
	}
	if strings.Contains(firstResponse.Body.String(), denied.Name) || strings.Contains(firstResponse.Body.String(), "private.bin") {
		t.Fatalf("denied repository leaked: %s", firstResponse.Body.String())
	}

	secondResponse := request("packages/", firstPage.NextPageToken)
	var secondPage searchPage
	if secondResponse.Code != http.StatusOK || json.Unmarshal(secondResponse.Body.Bytes(), &secondPage) != nil || len(secondPage.Items) != 1 || secondPage.Items[0].RepositoryID != last.ID || secondPage.NextPageToken != "" {
		t.Fatalf("second page=%d body=%s", secondResponse.Code, secondResponse.Body.String())
	}
	if changedQuery := request("other/", firstPage.NextPageToken); changedQuery.Code != http.StatusBadRequest || !strings.Contains(changedQuery.Body.String(), "invalid_page_token") {
		t.Fatalf("changed query=%d body=%s", changedQuery.Code, changedQuery.Body.String())
	}
}

func TestGlobalArtifactSearchRequiresAuthenticationAndNonEmptyQuery(t *testing.T) {
	handler := NewGatewayHandler(Dependencies{}, repository.NewMemoryStore(), TestAdapter{}, testAuthenticator())
	unauthenticated := httptest.NewRecorder()
	handler.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, "/api/v2/artifact-search?q=demo", nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
	}
	emptyRequest := httptest.NewRequest(http.MethodGet, "/api/v2/artifact-search?q=%20", nil)
	authorize(emptyRequest, "admin-secret")
	empty := httptest.NewRecorder()
	handler.ServeHTTP(empty, emptyRequest)
	if empty.Code != http.StatusBadRequest {
		t.Fatalf("empty query status=%d body=%s", empty.Code, empty.Body.String())
	}
}

func TestGlobalArtifactSearchFindsExactDigestWithOrWithoutAlgorithmPrefix(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: "00000000-0000-0000-0000-000000000010", Name: "checksums", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	digestHex := strings.Repeat("a", 64)
	digest := "sha256:" + digestHex
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{
		RepositoryID: repo.ID, Path: "releases/example.tar.gz", Digest: digest,
		ObjectKey: "raw/example", Size: 42, ContentType: "application/gzip",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, repository.RawAsset{
		RepositoryID: repo.ID, Path: "releases/other.tar.gz", Digest: "sha256:" + strings.Repeat("b", 64),
		ObjectKey: "raw/other", Size: 21, ContentType: "application/gzip",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{
		Principal: "search-user", Scopes: []string{"repositories:read"},
	}}, "1"); err != nil {
		t.Fatal(err)
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	search := func(query string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v2/artifact-search?q="+url.QueryEscape(query), nil)
		authorize(request, authenticator.IssueToken("search-user"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	for _, query := range []string{digest, strings.ToUpper(digestHex)} {
		response := search(query)
		var page struct {
			Items []struct {
				Coordinate string `json:"coordinate"`
				Digest     string `json:"digest"`
				MatchKind  string `json:"matchKind"`
			} `json:"items"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
			t.Fatalf("query=%q status=%d body=%s", query, response.Code, response.Body.String())
		}
		if len(page.Items) != 1 || page.Items[0].Coordinate != "releases/example.tar.gz" || page.Items[0].Digest != digest || page.Items[0].MatchKind != "digest" {
			t.Fatalf("query=%q page=%#v", query, page)
		}
	}

	coordinateResponse := search("releases/")
	if coordinateResponse.Code != http.StatusOK || !strings.Contains(coordinateResponse.Body.String(), `"matchKind":"coordinate"`) {
		t.Fatalf("coordinate status=%d body=%s", coordinateResponse.Code, coordinateResponse.Body.String())
	}
}

func TestParseGlobalArtifactSearchQueryRecognizesOnlyCompleteSHA256Digests(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, input := range []string{digest, "sha256:" + digest, "SHA256:" + strings.ToUpper(digest)} {
		query := parseGlobalArtifactSearchQuery(input)
		if query.Mode != repository.ArtifactSearchByDigest || query.Value != "sha256:"+digest {
			t.Fatalf("input=%q query=%#v", input, query)
		}
	}
	for _, input := range []string{"sha256:short", strings.Repeat("z", 64), "org.example:demo"} {
		query := parseGlobalArtifactSearchQuery(input)
		if query.Mode != repository.ArtifactSearchByCoordinate || query.Value != input {
			t.Fatalf("input=%q query=%#v", input, query)
		}
	}
}
