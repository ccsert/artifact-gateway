package repository

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestMemoryConanLifecyclePublishesAndTombstonesRevisions(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "repo", Name: "conan-hosted", Format: FormatConan}); err != nil {
		t.Fatal(err)
	}
	recipeObject := ConanObjectIntent{RepositoryID: "repo", ObjectKey: "native/conan/objects/recipe", Digest: "sha256:" + strings.Repeat("a", 64), Size: 3}
	if err := store.StageConanObject(ctx, recipeObject); err != nil {
		t.Fatal(err)
	}
	recipe := ConanRecipeRevision{RepositoryID: "repo", Reference: "pkg/1.0/user/stable", Revision: "rrev", Digest: recipeObject.Digest}
	asset := ConanAsset{RepositoryID: "repo", Reference: recipe.Reference, RecipeRevision: recipe.Revision, Path: "conanfile.py", ObjectKey: recipeObject.ObjectKey, Digest: recipeObject.Digest, Size: recipeObject.Size}
	published, err := store.PutConanRecipeRevision(ctx, recipe, []ConanAsset{asset})
	if err != nil || published.State != "visible" || published.CreatedAt.IsZero() {
		t.Fatalf("recipe=%#v err=%v", published, err)
	}
	if _, err = store.PutConanRecipeRevision(ctx, recipe, []ConanAsset{asset}); err != nil {
		t.Fatalf("recipe publish replay: %v", err)
	}
	packageObject := ConanObjectIntent{RepositoryID: "repo", ObjectKey: "native/conan/objects/package", Digest: "sha256:" + strings.Repeat("b", 64), Size: 7}
	if err = store.StageConanObject(ctx, packageObject); err != nil {
		t.Fatal(err)
	}
	pkg := ConanPackageRevision{RepositoryID: "repo", Reference: recipe.Reference, RecipeRevision: recipe.Revision, PackageID: "package-id", Revision: "prev", Digest: packageObject.Digest}
	pkgAsset := ConanAsset{RepositoryID: "repo", Reference: recipe.Reference, RecipeRevision: recipe.Revision, PackageID: pkg.PackageID, PackageRevision: pkg.Revision, Path: "package.tgz", ObjectKey: packageObject.ObjectKey, Digest: packageObject.Digest, Size: packageObject.Size}
	publishedPackage, err := store.PutConanPackageRevision(ctx, pkg, []ConanAsset{pkgAsset})
	if err != nil || publishedPackage.State != "visible" {
		t.Fatalf("package=%#v err=%v", publishedPackage, err)
	}
	ids, err := store.ListConanPackageIDs(ctx, "repo", recipe.Reference, recipe.Revision)
	if err != nil || len(ids) != 1 || ids[0] != pkg.PackageID {
		t.Fatalf("visible package IDs=%v err=%v", ids, err)
	}
	tombstonedPackage, err := store.TombstoneConanPackageRevision(ctx, "repo", recipe.Reference, recipe.Revision, pkg.PackageID, pkg.Revision)
	if err != nil || tombstonedPackage.State != "deleted" {
		t.Fatalf("package tombstone=%#v err=%v", tombstonedPackage, err)
	}
	ids, err = store.ListConanPackageIDs(ctx, "repo", recipe.Reference, recipe.Revision)
	if err != nil || len(ids) != 0 {
		t.Fatalf("tombstoned package IDs=%v err=%v", ids, err)
	}
	tombstonedRecipe, err := store.TombstoneConanRecipeRevision(ctx, "repo", recipe.Reference, recipe.Revision)
	if err != nil || tombstonedRecipe.State != "deleted" {
		t.Fatalf("recipe tombstone=%#v err=%v", tombstonedRecipe, err)
	}
	if _, err = store.GetArtifactTombstone(ctx, "repo", FormatConan, recipe.Reference+"#"+recipe.Revision); err != nil {
		t.Fatalf("recipe artifact tombstone: %v", err)
	}
}

func TestMemoryConanPackageRequiresVisibleRecipe(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.PutConanPackageRevision(context.Background(), ConanPackageRevision{RepositoryID: "repo", Reference: "pkg/1.0/user/stable", RecipeRevision: "missing", PackageID: "id", Revision: "prev", Digest: "sha256:" + strings.Repeat("c", 64)}, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("package without recipe err=%v", err)
	}
}
