package repository

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemoryArtifactIdentitiesDeduplicateCanonicalPyPIPairs(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "repo", Name: "pypi", Format: FormatPyPI}); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	for index, filename := range []string{"widget-1.0-py3-none-any.whl", "widget-1.0.tar.gz"} {
		if _, err := store.PublishPyPIFile(ctx, PyPIFile{
			RepositoryID: "repo", Project: "widget", Version: "1.0", Filename: filename,
			Digest: digest, ObjectKey: "pypi/" + filename, Size: 42, CreatedAt: time.Date(2026, 8, 10+index, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
	}

	identities, err := store.ListArtifactIdentities(ctx, "repo", FormatPyPI, ArtifactIdentityDistribution, digest, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 || identities[0].Coordinate != "widget@1.0" || identities[0].Digest != digest || identities[0].PublishedAt.Day() != 11 {
		t.Fatalf("identities=%#v", identities)
	}
}

func TestArtifactIdentityPurposeRejectsUnsupportedValues(t *testing.T) {
	_, err := NewMemoryStore().ListArtifactIdentities(context.Background(), "repo", FormatRaw, "browse", "", 50)
	if err == nil {
		t.Fatal("expected unsupported purpose error")
	}
}

func TestMemoryArtifactIdentitiesExcludeConanRevisionsWithoutResolvableAssets(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateHostedRepository(ctx, HostedRepository{ID: "repo", Name: "conan", Format: FormatConan}); err != nil {
		t.Fatal(err)
	}
	recipeDigest := "sha256:" + strings.Repeat("c", 64)
	packageDigest := "sha256:" + strings.Repeat("d", 64)
	if _, err := store.PutConanRecipeRevision(ctx, ConanRecipeRevision{RepositoryID: "repo", Reference: "empty/1.0", Revision: "rrev", Digest: recipeDigest}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: "repo", ObjectKey: "recipe-object", Digest: recipeDigest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanRecipeRevision(ctx, ConanRecipeRevision{RepositoryID: "repo", Reference: "ready/1.0", Revision: "rrev", Digest: recipeDigest}, []ConanAsset{{RepositoryID: "repo", Reference: "ready/1.0", RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: "recipe-object", Digest: recipeDigest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanPackageRevision(ctx, ConanPackageRevision{RepositoryID: "repo", Reference: "ready/1.0", RecipeRevision: "rrev", PackageID: "empty", Revision: "prev", Digest: packageDigest}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: "repo", ObjectKey: "package-object", Digest: packageDigest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanPackageRevision(ctx, ConanPackageRevision{RepositoryID: "repo", Reference: "ready/1.0", RecipeRevision: "rrev", PackageID: "ready", Revision: "prev", Digest: packageDigest}, []ConanAsset{{RepositoryID: "repo", Reference: "ready/1.0", RecipeRevision: "rrev", PackageID: "ready", PackageRevision: "prev", Path: "package.tgz", ObjectKey: "package-object", Digest: packageDigest, Size: 1}}); err != nil {
		t.Fatal(err)
	}

	scan, err := store.ListArtifactIdentities(ctx, "repo", FormatConan, ArtifactIdentityScan, "", 50)
	if err != nil || len(scan) != 2 || scan[0].Coordinate != "ready/1.0#rrev/ready#prev" && scan[1].Coordinate != "ready/1.0#rrev/ready#prev" {
		t.Fatalf("scan identities=%#v err=%v", scan, err)
	}
	distribution, err := store.ListArtifactIdentities(ctx, "repo", FormatConan, ArtifactIdentityDistribution, "", 50)
	if err != nil || len(distribution) != 1 || distribution[0].Coordinate != "ready/1.0#rrev" {
		t.Fatalf("distribution identities=%#v err=%v", distribution, err)
	}
}
