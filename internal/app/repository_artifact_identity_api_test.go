package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestRepositoryArtifactIdentitiesReturnHistoricalNPMVersions(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "npm-identities", Format: repository.FormatNPM,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{
		Principal: "identity-reader", Scopes: []string{"repositories:read"},
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	digest1 := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	digest2 := "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	for _, version := range []repository.NPMVersion{
		{RepositoryID: repo.ID, PackageName: "@team/widget", Version: "1.0.0", Digest: digest1, ObjectKey: "npm/widget/1", Size: 10, CreatedAt: time.Date(2026, 8, 10, 1, 0, 0, 0, time.UTC)},
		{RepositoryID: repo.ID, PackageName: "@team/widget", Version: "2.0.0", Digest: digest2, ObjectKey: "npm/widget/2", Size: 20, CreatedAt: time.Date(2026, 8, 11, 1, 0, 0, 0, time.UTC)},
	} {
		if _, err = store.PublishNPMVersion(ctx, version, map[string]string{"latest": version.Version}); err != nil {
			t.Fatal(err)
		}
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-identities?purpose=distribution&q=widget&pageSize=50", nil)
	authorize(request, authenticator.IssueToken("identity-reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var page struct {
		Items []struct {
			Coordinate string `json:"coordinate"`
			Digest     string `json:"digest"`
			Size       int64  `json:"size"`
		} `json:"items"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(page.Items) != 2 || page.Items[0].Coordinate != "@team/widget@2.0.0" || page.Items[0].Digest != digest2 || page.Items[0].Size != 20 || page.Items[1].Coordinate != "@team/widget@1.0.0" || page.Items[1].Digest != digest1 {
		t.Fatalf("identities=%#v", page.Items)
	}
}

func TestRepositoryArtifactIdentitiesApplyConanPurposeBoundary(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "conan-identities", Format: repository.FormatConan,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{
		Principal: "identity-reader", Scopes: []string{"repositories:read"},
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	reference, recipeRevision := "widget/1.0@team/stable", "recipe-revision"
	recipeDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	packageDigest := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{
		RepositoryID: repo.ID, Reference: reference, Revision: recipeRevision, Digest: recipeDigest,
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanPackageRevision(ctx, repository.ConanPackageRevision{
		RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision,
		PackageID: "package-id", Revision: "package-revision", Digest: packageDigest,
	}, nil); err != nil {
		t.Fatal(err)
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	list := func(purpose string) []struct {
		Coordinate string `json:"coordinate"`
		Digest     string `json:"digest"`
	} {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-identities?purpose="+purpose+"&q=widget", nil)
		authorize(request, authenticator.IssueToken("identity-reader"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var page struct {
			Items []struct {
				Coordinate string `json:"coordinate"`
				Digest     string `json:"digest"`
			} `json:"items"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
			t.Fatalf("purpose=%s status=%d body=%s", purpose, response.Code, response.Body.String())
		}
		return page.Items
	}

	scan := list("scan")
	if len(scan) != 2 {
		t.Fatalf("scan identities=%#v", scan)
	}
	scanByCoordinate := make(map[string]string, len(scan))
	for _, identity := range scan {
		scanByCoordinate[identity.Coordinate] = identity.Digest
	}
	if scanByCoordinate[reference+"#"+recipeRevision] != recipeDigest || scanByCoordinate[reference+"#"+recipeRevision+"/package-id#package-revision"] != packageDigest {
		t.Fatalf("scan identities=%#v", scanByCoordinate)
	}
	distribution := list("distribution")
	if len(distribution) != 1 || distribution[0].Coordinate != reference+"#"+recipeRevision || distribution[0].Digest != recipeDigest {
		t.Fatalf("distribution identities=%#v", distribution)
	}
}

func TestRepositoryArtifactIdentitiesRequireRepositoryRead(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{
		ID: uuid.NewString(), Name: "private-identities", Format: repository.FormatRaw,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{
		Principal: "identity-reader", Scopes: []string{"repositories:read"},
	}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	path := "/api/v2/repositories/" + repo.ID + "/artifact-identities?purpose=scan"

	for _, test := range []struct {
		name   string
		token  string
		status int
	}{
		{name: "anonymous", status: http.StatusUnauthorized},
		{name: "ungranted", token: authenticator.IssueToken("ungranted"), status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			if test.token != "" {
				authorize(request, test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRepositoryArtifactIdentitiesRejectOverlongQuery(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "query-limit", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repo.ID+"/artifact-identities?purpose=scan&q="+strings.Repeat("x", 256), nil)
	authorize(request, authenticator.IssueToken("reader"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_request") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRepositoryArtifactIdentitiesExcludeUncachedProxyMetadata(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	digest := "sha256:" + strings.Repeat("c", 64)
	repositories := []repository.HostedRepository{
		{ID: uuid.NewString(), Name: "npm-proxy-identities", Format: repository.FormatNPM, Type: repository.RepositoryTypeProxy, Endpoint: "https://registry.example"},
		{ID: uuid.NewString(), Name: "pypi-proxy-identities", Format: repository.FormatPyPI, Type: repository.RepositoryTypeProxy, Endpoint: "https://pypi.example"},
	}
	for index := range repositories {
		var err error
		repositories[index], err = store.CreateHostedRepository(ctx, repositories[index])
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.ReplaceRepositoryGrants(ctx, repositories[index].ID, []repository.RepositoryGrant{{Principal: "reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
			t.Fatal(err)
		}
	}
	npmVersion := repository.NPMVersion{Version: "1.0.0", UpstreamTarball: "https://registry.example/widget.tgz", TarballName: "widget.tgz", Manifest: []byte(`{"name":"widget","version":"1.0.0"}`)}
	if _, err := store.SyncNPMProxyPackage(ctx, repository.NPMPackage{RepositoryID: repositories[0].ID, Name: "widget", Versions: []repository.NPMVersion{npmVersion}, DistTags: map[string]string{"latest": "1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	pypiFile := repository.PyPIFile{Version: "1.0", Filename: "widget-1.0.whl", Digest: digest, SourceURL: "https://pypi.example/widget.whl"}
	if err := store.SyncPyPIProxyFiles(ctx, repositories[1].ID, "widget", []repository.PyPIFile{pypiFile}); err != nil {
		t.Fatal(err)
	}

	authenticator := testAuthenticator()
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	list := func(repositoryID string) int {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+repositoryID+"/artifact-identities?purpose=scan", nil)
		authorize(request, authenticator.IssueToken("reader"))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var page struct {
			Items []json.RawMessage `json:"items"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
		return len(page.Items)
	}
	if count := list(repositories[0].ID); count != 0 {
		t.Fatalf("uncached npm identities=%d", count)
	}
	if count := list(repositories[1].ID); count != 0 {
		t.Fatalf("uncached PyPI identities=%d", count)
	}
	if _, err := store.CacheNPMProxyTarball(ctx, repository.NPMVersion{RepositoryID: repositories[0].ID, PackageName: "widget", Version: "1.0.0", Digest: digest, ObjectKey: "npm/widget", Size: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CachePyPIProxyFile(ctx, repository.PyPIFile{RepositoryID: repositories[1].ID, Filename: pypiFile.Filename, Digest: digest, SourceURL: pypiFile.SourceURL, ObjectKey: "pypi/widget", Size: 20}); err != nil {
		t.Fatal(err)
	}
	if count := list(repositories[0].ID); count != 1 {
		t.Fatalf("cached npm identities=%d", count)
	}
	if count := list(repositories[1].ID); count != 1 {
		t.Fatalf("cached PyPI identities=%d", count)
	}
}
