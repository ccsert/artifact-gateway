package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	adminopenapi "github.com/ccsert/artifact-gateway/internal/admin/openapi"
	"github.com/ccsert/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

type artifactCatalogPort struct{ artifactRepositoryCatalog }
type artifactRawQueryPort struct{ artifactRawReader }
type artifactGroupQueryPort struct{ artifactGroupReader }
type artifactAnonymousPolicyPort struct {
	managementAnonymousPolicyReader
}

func TestArtifactManagementDomainRawReadsWithoutNativePublisher(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "domain-visible", Format: repository.FormatRaw, AnonymousRead: true})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "domain-hidden", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, fixture := range []struct{ repo, path string }{{repo.ID, "docs/first.txt"}, {repo.ID, "docs/second.txt"}, {hidden.ID, "docs/secret.txt"}} {
		if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: fixture.repo, Path: fixture.path, Digest: digest, ObjectKey: "raw/" + fixture.path, Size: 7, ContentType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.ReplaceRepositoryGrants(ctx, repo.ID, []repository.RepositoryGrant{{Principal: "artifact-reader", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	group, _, err := store.CreateHostedGroupIdempotently(ctx, repository.HostedGroup{ID: uuid.NewString(), Name: "domain-group", Format: repository.FormatRaw, AnonymousRead: true, Members: []repository.GroupMember{{RepositoryID: repo.ID, Position: 0}}}, "alice", "domain-group", "domain-group-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	enableAnonymousAccess(t, store)
	auth := testAuthenticator()
	auth.LegacyReadPermissive = false
	auth.RepositoryReaders = map[string][]string{}
	catalog := artifactCatalogPort{store}
	policy := artifactAnonymousPolicyPort{store}
	scope := newManagementScope(catalog, policy, store, &managementAuditFixture{}, auth, nil)
	domain := newArtifactManagementHandler(artifactManagementOptions{Scope: scope, Repositories: catalog, Groups: artifactGroupQueryPort{store}, Grants: store, Raw: artifactRawQueryPort{store}, Browse: store})
	// No native publisher, Maven store, object store or unrelated format dependency.
	server := adminopenapi.HandlerWithOptions(managementServer{artifactManagementHandler: domain}, adminopenapi.StdHTTPServerOptions{BaseURL: "/api/v2", BaseRouter: openAPIServeMux{mux: http.NewServeMux()}})
	token := auth.IssueToken("artifact-reader")
	call := func(token, path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			authorize(r, token)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	path := "/api/v2/repositories/" + repo.ID + "/artifact-search?q=docs/&pageSize=1"
	response := call(token, path)
	var first adminopenapi.ArtifactSummaryPage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &first) != nil || len(first.Items) != 1 || first.Items[0].Coordinate != "docs/first.txt" || first.NextPageToken == nil {
		t.Fatalf("first=%d %s", response.Code, response.Body.String())
	}
	response = call(token, path+"&pageToken="+url.QueryEscape(*first.NextPageToken))
	var second adminopenapi.ArtifactSummaryPage
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &second) != nil || len(second.Items) != 1 || second.Items[0].Coordinate != "docs/second.txt" || second.NextPageToken != nil {
		t.Fatalf("second=%d %s", response.Code, response.Body.String())
	}
	response = call(token, "/api/v2/artifact-search?q=docs/&format=raw")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "secret.txt") || !strings.Contains(response.Body.String(), "first.txt") {
		t.Fatalf("global scope=%d %s", response.Code, response.Body.String())
	}
	response = call("", "/api/v2/repositories/"+repo.ID+"/artifact-search?q=docs/")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "first.txt") {
		t.Fatalf("explicit readonly anonymous policy=%d %s", response.Code, response.Body.String())
	}
	for _, groupPath := range []string{"/api/v2/groups/" + group.ID + "/browse", "/api/v2/repositories/" + group.ID + "/artifact-search?q=docs/"} {
		response = call("", groupPath)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "docs") {
			t.Fatalf("readonly Group discovery %s=%d %s", groupPath, response.Code, response.Body.String())
		}
	}
	rootResponse, root := browseRepositoryForTest(t, server, token, repo.ID, "", "", 1)
	if rootResponse.Code != http.StatusOK || len(root.Items) != 1 || root.Items[0].Name != "docs" {
		t.Fatalf("browse=%d %+v", rootResponse.Code, root)
	}
	childResponse, child := browseRepositoryForTest(t, server, token, repo.ID, root.Items[0].ID, "", 50)
	if childResponse.Code != http.StatusOK || len(child.Items) != 2 || child.Items[0].Path != "docs/first.txt" {
		t.Fatalf("child=%d %+v", childResponse.Code, child)
	}
	for _, suffix := range []string{"artifact-identities?purpose=scan", "artifact-usage"} {
		response = call(token, "/api/v2/repositories/"+repo.ID+"/"+suffix)
		if response.Code != http.StatusNotImplemented || !strings.Contains(response.Body.String(), "not_supported") {
			t.Fatalf("optional %s=%d %s", suffix, response.Code, response.Body.String())
		}
	}
	if response = call("", "/api/v2/repositories/"+hidden.ID+"/artifact-search?q=docs/"); response.Code != http.StatusUnauthorized {
		t.Fatalf("private=%d %s", response.Code, response.Body.String())
	}
}
