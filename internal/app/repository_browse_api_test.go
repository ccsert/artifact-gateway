package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type repositoryBrowseTestNode struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	HasChildren bool   `json:"hasChildren"`
	Path        string `json:"path"`
	Digest      string `json:"digest"`
	Size        *int64 `json:"size"`
}

func TestRepositoryBrowseProjectsMavenNamespaceComponentVersionAndAssets(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "maven-releases", Format: repository.FormatMaven,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "maven-browser", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for index, fixture := range []struct {
		coordinate string
		path       string
	}{
		{"com.acme:widget:1.0", "com/acme/widget/1.0/widget-1.0.jar"},
		{"com.acme:widget:2.0", "com/acme/widget/2.0/widget-2.0.jar"},
		{"org.example:demo:3.0", "org/example/demo/3.0/demo-3.0.pom"},
	} {
		sessionID := uuid.NewString()
		if _, err = store.CreateMavenPublishSession(ctx, repository.MavenPublishSession{
			ID: sessionID, RepositoryID: repo.ID, Coordinate: fixture.coordinate, Publisher: "maven-browser", State: "open",
			ExpiresAt: time.Now().UTC().Add(time.Hour), Objects: []repository.MavenDeclaredObject{{Name: fixture.path, Digest: digest, Size: int64(index + 1)}},
		}); err != nil {
			t.Fatal(err)
		}
		if err = store.MarkMavenPublishObject(ctx, sessionID, fixture.path, "maven/object/"+sessionID); err != nil {
			t.Fatal(err)
		}
		if _, err = store.CommitMavenPublishSession(ctx, sessionID, []repository.MavenAsset{{RepositoryID: repo.ID, Path: fixture.path, ObjectKey: "maven/object/" + sessionID, Digest: digest, Size: int64(index + 1)}}); err != nil {
			t.Fatal(err)
		}
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	token := authenticator.IssueToken("maven-browser")
	_, root := browseRepositoryForTest(t, handler, token, repo.ID, "", "", 50)
	if len(root.Items) != 2 || root.Items[0].Name != "com.acme" || root.Items[1].Name != "org.example" {
		t.Fatalf("root=%+v", root)
	}
	_, components := browseRepositoryForTest(t, handler, token, repo.ID, root.Items[0].ID, "", 50)
	if len(components.Items) != 1 || components.Items[0].Kind != "component" || components.Items[0].Name != "widget" {
		t.Fatalf("components=%+v", components)
	}
	_, versions := browseRepositoryForTest(t, handler, token, repo.ID, components.Items[0].ID, "", 50)
	if len(versions.Items) != 2 || versions.Items[0].Kind != "version" || versions.Items[0].Name != "1.0" || versions.Items[0].HasChildren != true {
		t.Fatalf("versions=%+v", versions)
	}
	_, assets := browseRepositoryForTest(t, handler, token, repo.ID, versions.Items[0].ID, "", 50)
	if len(assets.Items) != 1 || assets.Items[0].Kind != "asset" || assets.Items[0].Name != "widget-1.0.jar" || assets.Items[0].Path != "com/acme/widget/1.0/widget-1.0.jar" || assets.Items[0].Digest != digest {
		t.Fatalf("assets=%+v", assets)
	}
}

func TestRepositoryBrowseProjectsMavenProxyCacheWithTheSameHierarchy(t *testing.T) {
	handler, store, _, _, mavenCache, _, _ := newCacheEntriesTestHandler(t)
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "maven-central", Format: repository.FormatMaven,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://repo.maven.apache.org/maven2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "proxy-browser", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"org/example/widget/1.0/widget-1.0.jar",
		"org/example/widget/1.0/widget-1.0.pom",
	} {
		if err = mavenCache.Store(ctx, mavenCache.Key(repo.Name, path), path, CachedMavenContent{
			Body: []byte(path), ContentType: "application/octet-stream", Member: "central", Endpoint: repo.Endpoint, Repository: repo.Name,
		}); err != nil {
			t.Fatal(err)
		}
	}
	token := testAuthenticator().IssueToken("proxy-browser")
	_, root := browseRepositoryForTest(t, handler, token, repo.ID, "", "", 50)
	if len(root.Items) != 1 || root.Items[0].Name != "org.example" {
		t.Fatalf("root=%+v", root)
	}
	_, components := browseRepositoryForTest(t, handler, token, repo.ID, root.Items[0].ID, "", 50)
	_, versions := browseRepositoryForTest(t, handler, token, repo.ID, components.Items[0].ID, "", 50)
	_, assets := browseRepositoryForTest(t, handler, token, repo.ID, versions.Items[0].ID, "", 50)
	if len(assets.Items) != 2 || assets.Items[0].Path != "org/example/widget/1.0/widget-1.0.jar" || assets.Items[1].Path != "org/example/widget/1.0/widget-1.0.pom" {
		t.Fatalf("assets=%+v", assets)
	}
}

func TestRepositoryBrowseProjectsRawProxyCacheAsPathSegments(t *testing.T) {
	handler, store, _, _, _, rawCache, _ := newCacheEntriesTestHandler(t)
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "raw-downloads", Format: repository.FormatRaw,
		Type: repository.RepositoryTypeProxy, Endpoint: "https://downloads.example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "proxy-browser", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	path := "docs/release%20notes.txt"
	if err = rawCache.Store(ctx, rawCache.Key(repo.Name, path, "origin", repo.Endpoint), RawContent{
		Body: []byte("release notes"), Repository: repo.Name, Path: path,
		Member: "origin", Endpoint: repo.Endpoint, ContentType: "text/plain", CacheQuotaBytes: 10000,
	}); err != nil {
		t.Fatal(err)
	}
	token := testAuthenticator().IssueToken("proxy-browser")
	_, root := browseRepositoryForTest(t, handler, token, repo.ID, "", "", 50)
	if len(root.Items) != 1 || root.Items[0].Name != "docs" || !root.Items[0].HasChildren {
		t.Fatalf("root=%+v", root)
	}
	_, children := browseRepositoryForTest(t, handler, token, repo.ID, root.Items[0].ID, "", 50)
	if len(children.Items) != 1 || children.Items[0].Name != "release notes.txt" || children.Items[0].Path != path || children.Items[0].Size == nil || *children.Items[0].Size != int64(len("release notes")) {
		t.Fatalf("children=%+v", children)
	}
}

type repositoryBrowseTestPage struct {
	Items         []repositoryBrowseTestNode `json:"items"`
	NextPageToken string                     `json:"nextPageToken"`
}

func browseRepositoryForTest(t *testing.T, handler http.Handler, token, repositoryID, parent, pageToken string, pageSize int) (*httptest.ResponseRecorder, repositoryBrowseTestPage) {
	t.Helper()
	query := url.Values{}
	if parent != "" {
		query.Set("parent", parent)
	}
	if pageToken != "" {
		query.Set("pageToken", pageToken)
	}
	if pageSize > 0 {
		query.Set("pageSize", strconv.Itoa(pageSize))
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repositoryID+"/browse?"+query.Encode(), nil)
	if token != "" {
		authorize(request, token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page repositoryBrowseTestPage
	if response.Code == http.StatusOK {
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode browse page: %v", err)
		}
	}
	return response, page
}

func TestRepositoryBrowseProjectsRawPathsWithoutClientReconstruction(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "raw-releases", Format: repository.FormatRaw, AnonymousRead: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{
		{Principal: "browse-reader", Scopes: []string{"repositories:read"}},
		{Principal: "other-reader", Scopes: []string{"repositories:read"}},
	}, "1"); err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for index, path := range []string{"docs/release%20notes.txt", "packages/widget.bin", "root.txt"} {
		if _, err = store.PutRawAsset(ctx, repository.RawAsset{
			RepositoryID: repo.ID,
			Path:         path,
			Digest:       digest,
			ObjectKey:    "raw/object/" + string(rune('a'+index)),
			Size:         int64(index),
			ContentType:  "application/octet-stream",
		}); err != nil {
			t.Fatal(err)
		}
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	token := authenticator.IssueToken("browse-reader")

	rootResponse, firstPage := browseRepositoryForTest(t, handler, token, repo.ID, "", "", 1)
	if rootResponse.Code != http.StatusOK {
		t.Fatalf("root status=%d body=%s", rootResponse.Code, rootResponse.Body.String())
	}
	if len(firstPage.Items) != 1 || firstPage.Items[0].Kind != "directory" || firstPage.Items[0].Name != "docs" || !firstPage.Items[0].HasChildren || firstPage.Items[0].ID == "" || firstPage.NextPageToken == "" {
		t.Fatalf("first root page=%+v", firstPage)
	}

	nextResponse, nextPage := browseRepositoryForTest(t, handler, token, repo.ID, "", firstPage.NextPageToken, 1)
	if nextResponse.Code != http.StatusOK || len(nextPage.Items) != 1 || nextPage.Items[0].Name != "packages" {
		t.Fatalf("next root page status=%d page=%+v", nextResponse.Code, nextPage)
	}

	childResponse, childPage := browseRepositoryForTest(t, handler, token, repo.ID, firstPage.Items[0].ID, "", 50)
	if childResponse.Code != http.StatusOK {
		t.Fatalf("child status=%d body=%s", childResponse.Code, childResponse.Body.String())
	}
	if len(childPage.Items) != 1 || childPage.Items[0].Kind != "asset" || childPage.Items[0].Name != "release notes.txt" || childPage.Items[0].Path != "docs/release%20notes.txt" || childPage.Items[0].Digest != digest || childPage.Items[0].Size == nil || *childPage.Items[0].Size != 0 || childPage.Items[0].HasChildren {
		t.Fatalf("child page=%+v", childPage)
	}

	anonymousResponse, anonymousPage := browseRepositoryForTest(t, handler, "", repo.ID, "", "", 50)
	if anonymousResponse.Code != http.StatusOK || len(anonymousPage.Items) != 3 {
		t.Fatalf("anonymous root status=%d page=%+v", anonymousResponse.Code, anonymousPage)
	}

	otherToken := authenticator.IssueToken("other-reader")
	wrongPrincipalResponse, _ := browseRepositoryForTest(t, handler, otherToken, repo.ID, "", firstPage.NextPageToken, 1)
	if wrongPrincipalResponse.Code != http.StatusBadRequest {
		t.Fatalf("cross-principal page token status=%d body=%s", wrongPrincipalResponse.Code, wrongPrincipalResponse.Body.String())
	}

	otherRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "raw-other", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, otherRepo.ID, []repository.RepositoryGrant{{Principal: "browse-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	wrongParentResponse, _ := browseRepositoryForTest(t, handler, token, otherRepo.ID, firstPage.Items[0].ID, "", 50)
	if wrongParentResponse.Code != http.StatusBadRequest {
		t.Fatalf("cross-repository parent status=%d body=%s", wrongParentResponse.Code, wrongParentResponse.Body.String())
	}
}
