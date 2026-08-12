package repository

import (
	"context"
	"strings"
	"testing"
)

func TestMemoryArtifactScanCandidatesIncludeConanRecipesAndPackages(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "repo", Name: "conan", Format: FormatConan}); err != nil {
		t.Fatal(err)
	}
	recipeDigest := "sha256:" + strings.Repeat("a", 64)
	packageDigest := "sha256:" + strings.Repeat("b", 64)
	if err := store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: "repo", ObjectKey: "recipe", Digest: recipeDigest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanRecipeRevision(ctx, ConanRecipeRevision{RepositoryID: "repo", Reference: "widget/1.0@team/stable", Revision: "rrev", Digest: recipeDigest}, []ConanAsset{{RepositoryID: "repo", Reference: "widget/1.0@team/stable", RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: "recipe", Digest: recipeDigest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: "repo", ObjectKey: "package", Digest: packageDigest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanPackageRevision(ctx, ConanPackageRevision{RepositoryID: "repo", Reference: "widget/1.0@team/stable", RecipeRevision: "rrev", PackageID: "pkg", Revision: "prev", Digest: packageDigest}, []ConanAsset{{RepositoryID: "repo", Reference: "widget/1.0@team/stable", RecipeRevision: "rrev", PackageID: "pkg", PackageRevision: "prev", Path: "package.tgz", ObjectKey: "package", Digest: packageDigest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.ListArtifactScanCandidates(ctx, "repo", FormatConan, 10)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	identities := map[string]string{}
	for _, candidate := range candidates {
		identities[candidate.Coordinate] = candidate.Digest
	}
	if identities["widget/1.0@team/stable#rrev"] != recipeDigest || identities["widget/1.0@team/stable#rrev/pkg#prev"] != packageDigest {
		t.Fatalf("identities=%#v", identities)
	}
}

func TestMemoryArtifactScanCandidatesPrioritizeMissingScans(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "repo", Name: "raw", Format: FormatRaw}); err != nil {
		t.Fatal(err)
	}
	missingDigest := "sha256:" + strings.Repeat("c", 64)
	activeDigest := "sha256:" + strings.Repeat("d", 64)
	if _, err := store.PutRawAsset(ctx, RawAsset{RepositoryID: "repo", Path: "older-missing.bin", Digest: missingDigest, ObjectKey: "raw/repo/older-missing.bin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutRawAsset(ctx, RawAsset{RepositoryID: "repo", Path: "newer-active.bin", Digest: activeDigest, ObjectKey: "raw/repo/newer-active.bin"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnqueueArtifactScanJob(ctx, store, "repo", "active", ArtifactScanPayload{
		Format: FormatRaw, Coordinate: "newer-active.bin", Digest: activeDigest,
	}); err != nil {
		t.Fatal(err)
	}

	candidates, err := store.ListArtifactScanCandidates(ctx, "repo", FormatRaw, 1)
	if err != nil || len(candidates) != 1 || candidates[0].Coordinate != "older-missing.bin" {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
}
