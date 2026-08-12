//go:build integration

package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresArtifactIdentitiesReturnCanonicalNPMVersions(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo, err := store.CreateHostedRepository(ctx, HostedRepository{
		ID: uuid.NewString(), Name: "artifact-identity-" + uuid.NewString(), Format: FormatNPM,
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM artifact_intelligence WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_npm_versions WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_npm_packages WHERE repository_id=$1`, repo.ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		_ = store.Close()
	})

	digest1 := "sha256:" + strings.Repeat("1", 64)
	digest2 := "sha256:" + strings.Repeat("2", 64)
	for _, version := range []NPMVersion{
		{RepositoryID: repo.ID, PackageName: "@team/widget", Version: "1.0.0", Digest: digest1, ObjectKey: "npm/widget/1", TarballName: "widget-1.0.0.tgz", Size: 10, Manifest: []byte(`{"name":"@team/widget","version":"1.0.0"}`)},
		{RepositoryID: repo.ID, PackageName: "@team/widget", Version: "2.0.0", Digest: digest2, ObjectKey: "npm/widget/2", TarballName: "widget-2.0.0.tgz", Size: 20, Manifest: []byte(`{"name":"@team/widget","version":"2.0.0"}`)},
	} {
		if _, err = store.PublishNPMVersion(ctx, version, map[string]string{"latest": version.Version}); err != nil {
			t.Fatal(err)
		}
		publishedAt := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
		if version.Version == "2.0.0" {
			publishedAt = publishedAt.Add(24 * time.Hour)
		}
		if _, err = store.db.ExecContext(ctx, `UPDATE native_npm_versions SET created_at=$4 WHERE repository_id=$1 AND package_name=$2 AND version=$3`, repo.ID, version.PackageName, version.Version, publishedAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = store.ReplaceArtifactIntelligence(ctx, ArtifactIntelligence{
		RepositoryID: repo.ID, Format: FormatNPM, Coordinate: "@team/widget@1.0.0", Digest: digest1,
		Vulnerability: &ArtifactVulnerabilitySummary{Status: "clean"}, UpdatedBy: "integration-test",
	}, ""); err != nil {
		t.Fatal(err)
	}

	identities, err := store.ListArtifactIdentities(ctx, repo.ID, FormatNPM, ArtifactIdentityDistribution, "widget", 50)
	if err != nil || len(identities) != 2 {
		t.Fatalf("identities=%#v err=%v", identities, err)
	}
	if identities[0].Coordinate != "@team/widget@2.0.0" || identities[0].Digest != digest2 || identities[0].Size == nil || *identities[0].Size != 20 {
		t.Fatalf("latest identity=%#v", identities[0])
	}
	if identities[1].Coordinate != "@team/widget@1.0.0" || identities[1].Intelligence == nil || identities[1].Intelligence.VulnerabilityStatus != "clean" {
		t.Fatalf("historical identity=%#v", identities[1])
	}
}

func TestPostgresArtifactIdentitiesExcludeUncachedProxyMetadata(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repositories := make([]HostedRepository, 0, 2)
	for _, format := range []Format{FormatNPM, FormatPyPI} {
		repo, createErr := store.CreateHostedRepository(ctx, HostedRepository{
			ID: uuid.NewString(), Name: "identity-proxy-" + string(format) + "-" + uuid.NewString()[:8], Format: format, Type: RepositoryTypeProxy, Endpoint: "https://proxy.example",
		})
		if createErr != nil {
			_ = store.Close()
			t.Fatal(createErr)
		}
		repositories = append(repositories, repo)
	}
	t.Cleanup(func() {
		for _, repo := range repositories {
			_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		}
		_ = store.Close()
	})

	digest := "sha256:" + strings.Repeat("c", 64)
	npmVersion := NPMVersion{Version: "1.0.0", UpstreamTarball: "https://registry.example/widget.tgz", TarballName: "widget.tgz", Manifest: []byte(`{"name":"widget","version":"1.0.0"}`)}
	if _, err = store.SyncNPMProxyPackage(ctx, NPMPackage{RepositoryID: repositories[0].ID, Name: "widget", SourceEndpoint: repositories[0].Endpoint, Versions: []NPMVersion{npmVersion}, DistTags: map[string]string{"latest": "1.0.0"}}); err != nil {
		t.Fatal(err)
	}
	pypiFile := PyPIFile{Version: "1.0", Filename: "widget-1.0.whl", Digest: digest, SourceURL: "https://pypi.example/widget.whl"}
	if err = store.SyncPyPIProxyFiles(ctx, repositories[1].ID, "widget", []PyPIFile{pypiFile}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range repositories {
		identities, listErr := store.ListArtifactIdentities(ctx, repo.ID, repo.Format, ArtifactIdentityScan, "", 50)
		if listErr != nil || len(identities) != 0 {
			t.Fatalf("uncached %s identities=%#v err=%v", repo.Format, identities, listErr)
		}
	}

	if _, err = store.CacheNPMProxyTarball(ctx, NPMVersion{RepositoryID: repositories[0].ID, PackageName: "widget", Version: "1.0.0", Digest: digest, ObjectKey: "npm/widget", Size: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CachePyPIProxyFile(ctx, PyPIFile{RepositoryID: repositories[1].ID, Filename: pypiFile.Filename, Digest: digest, SourceURL: pypiFile.SourceURL, ObjectKey: "pypi/widget", Size: 20}); err != nil {
		t.Fatal(err)
	}
	for _, repo := range repositories {
		identities, listErr := store.ListArtifactIdentities(ctx, repo.ID, repo.Format, ArtifactIdentityScan, "", 50)
		if listErr != nil || len(identities) != 1 || identities[0].Coordinate != "widget@1.0.0" && identities[0].Coordinate != "widget@1.0" {
			t.Fatalf("cached %s identities=%#v err=%v", repo.Format, identities, listErr)
		}
	}
	if err = store.StoreNPMProxyNegative(ctx, NPMPackage{RepositoryID: repositories[0].ID, Name: "widget", SourceEndpoint: repositories[0].Endpoint, NegativeExpiresAt: time.Now().UTC().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	identities, err := store.ListArtifactIdentities(ctx, repositories[0].ID, FormatNPM, ArtifactIdentityScan, "", 50)
	if err != nil || len(identities) != 0 {
		t.Fatalf("negative-cached npm identities=%#v err=%v", identities, err)
	}
}

func TestPostgresArtifactIdentitiesUseResolvableRawAndConanAssets(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repositories := make([]HostedRepository, 0, 3)
	for _, format := range []Format{FormatRaw, FormatRaw, FormatConan} {
		repo, createErr := store.CreateHostedRepository(ctx, HostedRepository{ID: uuid.NewString(), Name: "identity-assets-" + string(format) + "-" + uuid.NewString()[:8], Format: format})
		if createErr != nil {
			_ = store.Close()
			t.Fatal(createErr)
		}
		repositories = append(repositories, repo)
	}
	t.Cleanup(func() {
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_conan_assets WHERE repository_id=$1`, repositories[2].ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_conan_package_revisions WHERE repository_id=$1`, repositories[2].ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_conan_recipe_revisions WHERE repository_id=$1`, repositories[2].ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_conan_object_intents WHERE repository_id=$1`, repositories[2].ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_raw_assets WHERE repository_id=$1 OR repository_id=$2`, repositories[0].ID, repositories[1].ID)
		_, _ = store.db.ExecContext(context.Background(), `DELETE FROM native_raw_objects WHERE repository_id=$1 OR repository_id=$2`, repositories[0].ID, repositories[1].ID)
		for _, repo := range repositories {
			_, _ = store.db.ExecContext(context.Background(), `DELETE FROM hosted_repositories WHERE id=$1`, repo.ID)
		}
		_ = store.Close()
	})

	sharedDigest := "sha256:" + strings.Repeat("e", 64)
	if _, err = store.PutRawAsset(ctx, RawAsset{RepositoryID: repositories[0].ID, Path: "first.bin", Digest: sharedDigest, ObjectKey: "raw/shared", Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutRawAsset(ctx, RawAsset{RepositoryID: repositories[1].ID, Path: "second.bin", Digest: sharedDigest, ObjectKey: "raw/shared", Size: 5}); err != nil {
		t.Fatal(err)
	}
	rawIdentities, err := store.ListArtifactIdentities(ctx, repositories[0].ID, FormatRaw, ArtifactIdentityScan, "", 50)
	if err != nil || len(rawIdentities) != 1 || rawIdentities[0].Coordinate != "first.bin" {
		t.Fatalf("first repository raw identities=%#v err=%v", rawIdentities, err)
	}

	conanRepo := repositories[2]
	recipeDigest := "sha256:" + strings.Repeat("f", 64)
	packageDigest := "sha256:" + strings.Repeat("1", 64)
	if _, err = store.PutConanRecipeRevision(ctx, ConanRecipeRevision{RepositoryID: conanRepo.ID, Reference: "empty/1.0", Revision: "rrev", Digest: recipeDigest}, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: conanRepo.ID, ObjectKey: "conan/recipe", Digest: recipeDigest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanRecipeRevision(ctx, ConanRecipeRevision{RepositoryID: conanRepo.ID, Reference: "ready/1.0", Revision: "rrev", Digest: recipeDigest}, []ConanAsset{{RepositoryID: conanRepo.ID, Reference: "ready/1.0", RecipeRevision: "rrev", Path: "conanfile.py", ObjectKey: "conan/recipe", Digest: recipeDigest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanPackageRevision(ctx, ConanPackageRevision{RepositoryID: conanRepo.ID, Reference: "ready/1.0", RecipeRevision: "rrev", PackageID: "empty", Revision: "prev", Digest: packageDigest}, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.StageConanObject(ctx, ConanObjectIntent{RepositoryID: conanRepo.ID, ObjectKey: "conan/package", Digest: packageDigest, Size: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PutConanPackageRevision(ctx, ConanPackageRevision{RepositoryID: conanRepo.ID, Reference: "ready/1.0", RecipeRevision: "rrev", PackageID: "ready", Revision: "prev", Digest: packageDigest}, []ConanAsset{{RepositoryID: conanRepo.ID, Reference: "ready/1.0", RecipeRevision: "rrev", PackageID: "ready", PackageRevision: "prev", Path: "package.tgz", ObjectKey: "conan/package", Digest: packageDigest, Size: 1}}); err != nil {
		t.Fatal(err)
	}
	conanIdentities, err := store.ListArtifactIdentities(ctx, conanRepo.ID, FormatConan, ArtifactIdentityScan, "", 50)
	if err != nil || len(conanIdentities) != 2 {
		t.Fatalf("conan identities=%#v err=%v", conanIdentities, err)
	}
	distributionIdentities, err := store.ListArtifactIdentities(ctx, conanRepo.ID, FormatConan, ArtifactIdentityDistribution, "", 50)
	if err != nil || len(distributionIdentities) != 0 {
		t.Fatalf("Conan distribution identities with incomplete package closure=%#v err=%v", distributionIdentities, err)
	}
}
