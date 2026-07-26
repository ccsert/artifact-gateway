//go:build integration

package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/google/uuid"
)

func TestPostgresNativeConanLifecycleStateTransitions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-native-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	recipeObject := repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: "native/conan/objects/" + uuid.NewString(), Digest: "sha256:" + strings.Repeat("d", 64), Size: 3}
	if err = store.StageConanObject(ctx, recipeObject); err != nil {
		t.Fatal(err)
	}
	recipe := repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: "pkg/1.0/user/stable", Revision: "rrev", Digest: recipeObject.Digest}
	recipeAsset := repository.ConanAsset{RepositoryID: repo.ID, Reference: recipe.Reference, RecipeRevision: recipe.Revision, Path: "conanfile.py", ObjectKey: recipeObject.ObjectKey, Digest: recipeObject.Digest, Size: recipeObject.Size}
	publishedRecipe, err := store.PutConanRecipeRevision(ctx, recipe, []repository.ConanAsset{recipeAsset})
	if err != nil || publishedRecipe.State != "visible" || publishedRecipe.CreatedAt.IsZero() {
		t.Fatalf("recipe=%#v err=%v", publishedRecipe, err)
	}
	packageObject := repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: "native/conan/objects/" + uuid.NewString(), Digest: "sha256:" + strings.Repeat("e", 64), Size: 9}
	if err = store.StageConanObject(ctx, packageObject); err != nil {
		t.Fatal(err)
	}
	pkg := repository.ConanPackageRevision{RepositoryID: repo.ID, Reference: recipe.Reference, RecipeRevision: recipe.Revision, PackageID: "package-id", Revision: "prev", Digest: packageObject.Digest}
	pkgAsset := repository.ConanAsset{RepositoryID: repo.ID, Reference: recipe.Reference, RecipeRevision: recipe.Revision, PackageID: pkg.PackageID, PackageRevision: pkg.Revision, Path: "package.tgz", ObjectKey: packageObject.ObjectKey, Digest: packageObject.Digest, Size: packageObject.Size}
	publishedPackage, err := store.PutConanPackageRevision(ctx, pkg, []repository.ConanAsset{pkgAsset})
	if err != nil || publishedPackage.State != "visible" {
		t.Fatalf("package=%#v err=%v", publishedPackage, err)
	}
	if _, err = store.PutConanPackageRevision(ctx, pkg, []repository.ConanAsset{pkgAsset}); err != nil {
		t.Fatalf("package publish replay: %v", err)
	}
	tombstonedPackage, err := store.TombstoneConanPackageRevision(ctx, repo.ID, recipe.Reference, recipe.Revision, pkg.PackageID, pkg.Revision)
	if err != nil || tombstonedPackage.State != "deleted" {
		t.Fatalf("package tombstone=%#v err=%v", tombstonedPackage, err)
	}
	tombstonedRecipe, err := store.TombstoneConanRecipeRevision(ctx, repo.ID, recipe.Reference, recipe.Revision)
	if err != nil || tombstonedRecipe.State != "deleted" {
		t.Fatalf("recipe tombstone=%#v err=%v", tombstonedRecipe, err)
	}
}

func TestPostgresConanReferenceSearchProjection(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := repository.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, repository.HostedRepository{ID: uuid.NewString(), Name: "conan-search-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], Format: repository.FormatConan})
	if err != nil {
		t.Fatal(err)
	}
	for i, reference := range []string{"pkg/2.0/user/stable", "pkg/1.0/user/stable", "other/1.0/user/stable"} {
		key := "native/conan/search/" + uuid.NewString()
		digest := "sha256:" + strings.Repeat(string(rune('a'+i)), 64)
		if err = store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: repo.ID, ObjectKey: key, Digest: digest, Size: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err = store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: repo.ID, Reference: reference, Revision: "rrev", Digest: digest}, []repository.ConanAsset{{RepositoryID: repo.ID, Reference: reference, RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: key, Digest: digest, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	references, err := store.SearchConanReferences(ctx, repo.ID, "pkg/", 2, "")
	if err != nil || len(references) != 2 || references[0] != "pkg/1.0/user/stable" || references[1] != "pkg/2.0/user/stable" {
		t.Fatalf("references=%#v err=%v", references, err)
	}
	next, err := store.SearchConanReferences(ctx, repo.ID, "pkg/", 2, references[0])
	if err != nil || len(next) != 1 || next[0] != "pkg/2.0/user/stable" {
		t.Fatalf("next=%#v err=%v", next, err)
	}
}
