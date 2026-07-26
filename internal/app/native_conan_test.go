package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
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
