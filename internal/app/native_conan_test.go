package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestNativeConanHostedReadsRecipeRevisionWithoutGroup(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "repo", Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	body := []byte("recipe")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	objectKey := "native/conan/objects/recipe"
	if err = store.StageConanObject(context.Background(), repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: objectKey, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if err = objects.PutVerifiedReader(context.Background(), objectKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	reference := "pkg/1.0/user/stable"
	revision := repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: "rrev", Digest: digest}
	asset := repository.ConanAsset{RepositoryID: repo.ID, Reference: reference, RecipeRevision: revision.Revision, Path: "conanfile.py", ObjectKey: objectKey, Digest: digest, Size: int64(len(body))}
	if _, err = store.PutConanRecipeRevision(context.Background(), revision, []repository.ConanAsset{asset}); err != nil {
		t.Fatal(err)
	}
	handler := ConanHandler{Store: store, NativeStore: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: testAuthenticator()}, Authenticator: testAuthenticator(), NativeObjects: objects, Client: &conanAnonymousClient{}, Cache: NewConanCache(nil)}

	for _, test := range []struct {
		path string
		want string
	}{
		{"/conan/v2/conan-hosted/conans/pkg/1.0/user/stable/revisions", `"revision":"rrev"`},
		{"/conan/v2/conan-hosted/conans/pkg/1.0/user/stable/revisions/rrev/files", `"conanfile.py"`},
		{"/conan/v2/conan-hosted/conans/pkg/1.0/user/stable/revisions/rrev/files/conanfile.py", "recipe"},
	} {
		recorder := httptest.NewRecorder()
		request := conanRequest(test.path)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), test.want) {
			t.Fatalf("%s status=%d body=%s", test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestNativeConanHostedDeletesRecipeRevisionWithWriteGrant(t *testing.T) {
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(context.Background(), repository.HostedRepository{ID: "repo", Name: "conan-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReplaceRepositoryGrants(context.Background(), repo.ID, []repository.RepositoryGrant{{Principal: "build-agent", Scopes: []string{"repositories:read", "repositories:write"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	body := []byte("recipe")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	objectKey := "native/conan/objects/delete-recipe"
	if err = store.StageConanObject(context.Background(), repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: objectKey, Digest: digest, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if err = objects.PutVerifiedReader(context.Background(), objectKey, bytes.NewReader(body), int64(len(body)), digest); err != nil {
		t.Fatal(err)
	}
	reference := "pkg/1.0/user/stable"
	revision := repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: "rrev", Digest: digest}
	asset := repository.ConanAsset{RepositoryID: repo.ID, Reference: reference, RecipeRevision: revision.Revision, Path: "conanfile.py", ObjectKey: objectKey, Digest: digest, Size: int64(len(body))}
	if _, err = store.PutConanRecipeRevision(context.Background(), revision, []repository.ConanAsset{asset}); err != nil {
		t.Fatal(err)
	}
	handler := ConanHandler{Store: store, NativeStore: store, Repositories: store, Authorizer: RepositoryAuthorizer{Grants: store, Legacy: testAuthenticator()}, Authenticator: testAuthenticator(), NativeObjects: objects, Client: &conanAnonymousClient{}}
	request := httptest.NewRequest(http.MethodDelete, "/conan/v2/conan-hosted/conans/pkg/1.0/user/stable/revisions/rrev", nil)
	request.Header.Set("Authorization", "Bearer resolver-secret")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if _, err = store.GetArtifactTombstone(context.Background(), repo.ID, repository.FormatConan, reference+"#rrev"); err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	read := httptest.NewRecorder()
	readRequest := conanRequest("/conan/v2/conan-hosted/conans/pkg/1.0/user/stable/revisions/rrev/files/conanfile.py")
	readRequest.Header.Set("Authorization", "Bearer resolver-secret")
	handler.ServeHTTP(read, readRequest)
	if read.Code != http.StatusNotFound {
		t.Fatalf("read after delete status=%d body=%s", read.Code, read.Body.String())
	}
	restore := httptest.NewRecorder()
	restoreRequest := httptest.NewRequest(http.MethodPost, "/conan/v2/conan-hosted/conans/pkg/1.0/user/stable/revisions/rrev:restore", nil)
	restoreRequest.Header.Set("Authorization", "Bearer resolver-secret")
	handler.ServeHTTP(restore, restoreRequest)
	if restore.Code != http.StatusNoContent {
		t.Fatalf("restore status=%d body=%s", restore.Code, restore.Body.String())
	}
	read = httptest.NewRecorder()
	handler.ServeHTTP(read, readRequest)
	if read.Code != http.StatusOK || read.Body.String() != string(body) {
		t.Fatalf("read after restore status=%d body=%s", read.Code, read.Body.String())
	}
}

func TestNativeConanReferenceBrowseSearchProjection(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	conanRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-search", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	rawRepo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "raw-search", Format: repository.FormatRaw})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := testAuthenticator()
	if _, err = store.ReplaceRepositoryGrants(ctx, conanRepo.ID, []repository.RepositoryGrant{{Principal: "conan-browser", Scopes: []string{"repositories:read"}}}, "1"); err != nil {
		t.Fatal(err)
	}
	for i, reference := range []string{"pkg/1.0/user/stable", "pkg/2.0/user/stable", "other/1.0/user/stable"} {
		key := "native/conan/search/" + string(rune('a'+i))
		digest := "sha256:" + strings.Repeat(string(rune('a'+i)), 64)
		if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: conanRepo.ID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: conanRepo.ID, Reference: reference, Revision: "rrev", Digest: digest}, []repository.ConanAsset{{RepositoryID: conanRepo.ID, Reference: reference, RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewGatewayHandler(Dependencies{}, store, TestAdapter{}, authenticator)
	browserToken := authenticator.IssueToken("conan-browser")
	request := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+conanRepo.ID+"/conan/references?q=pkg%2F&pageSize=1", nil)
	authorize(request, browserToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page struct {
		Items []struct {
			Reference string `json:"reference"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Items[0].Reference != "pkg/1.0/user/stable" || page.NextPageToken == "" {
		t.Fatalf("browse=%d body=%s page=%#v", response.Code, response.Body.String(), page)
	}
	next := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+conanRepo.ID+"/conan/references?q=pkg%2F&pageSize=1&pageToken="+url.QueryEscape(page.NextPageToken), nil)
	authorize(next, browserToken)
	nextResponse := httptest.NewRecorder()
	handler.ServeHTTP(nextResponse, next)
	if nextResponse.Code != http.StatusOK || !strings.Contains(nextResponse.Body.String(), `"pkg/2.0/user/stable"`) {
		t.Fatalf("next=%d %s", nextResponse.Code, nextResponse.Body.String())
	}
	crossQuery := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+conanRepo.ID+"/conan/references?q=other%2F&pageToken="+url.QueryEscape(page.NextPageToken), nil)
	authorize(crossQuery, browserToken)
	crossQueryResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossQueryResponse, crossQuery)
	if crossQueryResponse.Code != http.StatusBadRequest || !strings.Contains(crossQueryResponse.Body.String(), "invalid_page_token") {
		t.Fatalf("cross query=%d %s", crossQueryResponse.Code, crossQueryResponse.Body.String())
	}
	nonConan := httptest.NewRequest(http.MethodGet, "/api/v2/repositories/"+rawRepo.ID+"/conan/references", nil)
	authorize(nonConan, browserToken)
	nonConanResponse := httptest.NewRecorder()
	handler.ServeHTTP(nonConanResponse, nonConan)
	if nonConanResponse.Code != http.StatusNotFound {
		t.Fatalf("non Conan=%d %s", nonConanResponse.Code, nonConanResponse.Body.String())
	}
}
