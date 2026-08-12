package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
