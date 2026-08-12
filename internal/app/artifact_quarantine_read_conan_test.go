package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func publishQuarantineReadConan(t *testing.T, store *repository.MemoryStore, objects *MemoryOCIObjectStore, repo repository.HostedRepository, reference, recipeRevision string, marker byte) repository.ConanRecipeRevision {
	t.Helper()
	put := func(path string, body []byte) repository.ConanAsset {
		sum := sha256.Sum256(body)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		key := "native/conan/sha256/" + hex.EncodeToString(sum[:])
		if err := objects.Put(context.Background(), key, body); err != nil {
			t.Fatal(err)
		}
		if err := store.StageConanObject(context.Background(), repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		return repository.ConanAsset{RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision, Path: path, ObjectKey: key, Digest: digest, Size: int64(len(body))}
	}
	recipeAsset := put("conanfile.py", []byte{marker, 'r'})
	recipe, err := store.PutConanRecipeRevision(context.Background(), repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: recipeRevision, Digest: recipeAsset.Digest}, []repository.ConanAsset{recipeAsset})
	if err != nil {
		t.Fatal(err)
	}
	packageAsset := put("conan_package.tgz", []byte{marker, 'p'})
	packageAsset.PackageID = "package-id"
	packageAsset.PackageRevision = "prev"
	if _, err = store.PutConanPackageRevision(context.Background(), repository.ConanPackageRevision{RepositoryID: repo.ID, Reference: reference, RecipeRevision: recipeRevision, PackageID: "package-id", Revision: "prev", Digest: packageAsset.Digest}, []repository.ConanAsset{packageAsset}); err != nil {
		t.Fatal(err)
	}
	return recipe
}

func TestConanQuarantineReadPolicyBlocksRecipeAndPackageClosureAndHidesRevision(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "conan-read-hosted", Name: "conan-read-hosted", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	reference, rrev := "pkg/1.0/user/stable", "rrev"
	recipe := publishQuarantineReadConan(t, store, objects, repo, reference, rrev, 'a')
	enableQuarantineReadPolicy(t, store, repo.ID)
	quarantineReadIdentity(t, store, repo, reference+"#"+rrev, recipe.Digest)
	handler := NewGatewayHandler(Dependencies{NativeConanObjectStore: objects}, store, TestAdapter{}, testAuthenticator())

	base := "/conan/v2/" + repo.Name + "/conans/" + reference + "/revisions"
	for _, path := range []string{base + "/" + rrev + "/files/conanfile.py", base + "/" + rrev + "/packages/package-id/revisions/prev/files/conan_package.tgz"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			request := httptest.NewRequest(method, path, nil)
			authorize(request, "resolver-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), repository.ArtifactQuarantinedReason) {
				t.Fatalf("%s closure %s=%d body=%q", method, path, response.Code, response.Body.String())
			}
		}
	}
	request := httptest.NewRequest(http.MethodGet, base, nil)
	authorize(request, "resolver-secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), rrev) {
		t.Fatalf("revision list=%d body=%q", response.Code, response.Body.String())
	}
	requireQuarantineReadDeniedAudit(t, store, repository.FormatConan)
}

func TestConanGroupDoesNotFallThroughPastQuarantinedRecipeRevision(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	first, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "conan-read-first", Name: "conan-read-first", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: "conan-read-second", Name: "conan-read-second", Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	reference, rrev := "pkg/1.0/user/stable", "rrev"
	recipe := publishQuarantineReadConan(t, store, objects, first, reference, rrev, 'a')
	publishQuarantineReadConan(t, store, objects, second, reference, rrev, 'b')
	enableQuarantineReadPolicy(t, store, first.ID)
	quarantineReadIdentity(t, store, first, reference+"#"+rrev, recipe.Digest)
	createV2Group(t, store, "conan-read-group", repository.FormatConan,
		repository.GroupMember{RepositoryID: first.ID},
		repository.GroupMember{RepositoryID: second.ID, Position: 1},
	)
	handler := NewGatewayHandler(Dependencies{NativeConanObjectStore: objects}, store, TestAdapter{}, testAuthenticator())
	base := "/conan/v2/conan-read-group/conans/" + reference + "/revisions"

	listRequest := httptest.NewRequest(http.MethodGet, base, nil)
	authorize(listRequest, "resolver-secret")
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), rrev) {
		t.Fatalf("group revisions=%d body=%q", listResponse.Code, listResponse.Body.String())
	}
	fileRequest := httptest.NewRequest(http.MethodGet, base+"/"+rrev+"/files/conanfile.py", nil)
	authorize(fileRequest, "resolver-secret")
	fileResponse := httptest.NewRecorder()
	handler.ServeHTTP(fileResponse, fileRequest)
	if fileResponse.Code != http.StatusForbidden || !strings.Contains(fileResponse.Body.String(), repository.ArtifactQuarantinedReason) {
		t.Fatalf("group file=%d body=%q", fileResponse.Code, fileResponse.Body.String())
	}
}
