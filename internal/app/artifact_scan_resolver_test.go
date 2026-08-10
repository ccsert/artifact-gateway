package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
	"github.com/artifact-gateway/artifact-gateway/internal/scanning"
)

func TestNativeArtifactScanResolverResolvesSingleFileFormats(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	resolver := NewNativeArtifactScanResolver(store, objects)

	rawDigest := putScanFixtureObject(t, objects, "scan/raw", "raw payload")
	if _, err := store.PutRawAsset(ctx, repository.RawAsset{RepositoryID: "raw", Path: "release/widget.bin", Digest: rawDigest, ObjectKey: "scan/raw", Size: 11, ContentType: "application/octet-stream"}); err != nil {
		t.Fatal(err)
	}
	npmDigest := putScanFixtureObject(t, objects, "scan/npm", "npm tarball")
	if _, err := store.PublishNPMVersion(ctx, repository.NPMVersion{RepositoryID: "npm", PackageName: "@team/widget", Version: "1.2.3", Digest: npmDigest, ObjectKey: "scan/npm", TarballName: "widget-1.2.3.tgz", Size: 11}, map[string]string{"latest": "1.2.3"}); err != nil {
		t.Fatal(err)
	}
	pypiDigest := putScanFixtureObject(t, objects, "scan/pypi-wheel", "pypi wheel")
	otherPyPIDigest := putScanFixtureObject(t, objects, "scan/pypi-source", "pypi source")
	if _, err := store.PublishPyPIVersion(ctx, []repository.PyPIFile{
		{RepositoryID: "pypi", Project: "widget", Version: "1.2.3", Filename: "widget-1.2.3.whl", Digest: pypiDigest, ObjectKey: "scan/pypi-wheel", Size: 10},
		{RepositoryID: "pypi", Project: "widget", Version: "1.2.3", Filename: "widget-1.2.3.tar.gz", Digest: otherPyPIDigest, ObjectKey: "scan/pypi-source", Size: 11},
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		repo    string
		payload repository.ArtifactScanPayload
	}{
		{name: "raw", repo: "raw", payload: repository.ArtifactScanPayload{Format: repository.FormatRaw, Coordinate: "release/widget.bin", Digest: rawDigest}},
		{name: "scoped npm", repo: "npm", payload: repository.ArtifactScanPayload{Format: repository.FormatNPM, Coordinate: "@team/widget@1.2.3", Digest: npmDigest}},
		{name: "one PyPI distribution", repo: "pypi", payload: repository.ArtifactScanPayload{Format: repository.FormatPyPI, Coordinate: "widget@1.2.3", Digest: pypiDigest}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			artifact, err := resolver.ResolveArtifactScan(ctx, testCase.repo, testCase.payload)
			if err != nil {
				t.Fatal(err)
			}
			assertScanAssetsReadable(t, ctx, artifact.Assets, 1)
			if artifact.Assets[0].Digest != testCase.payload.Digest {
				t.Fatalf("asset digest=%q want=%q", artifact.Assets[0].Digest, testCase.payload.Digest)
			}
			invalid := testCase.payload
			invalid.Digest = testScanDigest("different")
			if _, err := resolver.ResolveArtifactScan(ctx, testCase.repo, invalid); err == nil {
				t.Fatal("ResolveArtifactScan() accepted a changed digest")
			}
		})
	}
}

func TestNativeArtifactScanResolverWalksOCIIndex(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	resolver := NewNativeArtifactScanResolver(store, objects)

	configDigest := putScanFixtureObject(t, objects, "scan/oci-config", `{"architecture":"amd64"}`)
	layerDigest := putScanFixtureObject(t, objects, "scan/oci-layer", "layer bytes")
	registerScanOCIBlob(t, ctx, store, "oci", "config", "scan/oci-config", configDigest, 24)
	registerScanOCIBlob(t, ctx, store, "oci", "layer", "scan/oci-layer", layerDigest, 11)
	childBody, err := json.Marshal(scanOCIDocument{
		Config: &scanOCIDescriptor{Digest: configDigest, Size: 24, MediaType: "application/vnd.oci.image.config.v1+json"},
		Layers: []scanOCIDescriptor{{Digest: layerDigest, Size: 11, MediaType: "application/vnd.oci.image.layer.v1.tar"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	childDigest := putScanFixtureObject(t, objects, "scan/oci-child", string(childBody))
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: "oci", Name: "team/widget", Digest: childDigest, ObjectKey: "scan/oci-child", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(childBody))}, childDigest); err != nil {
		t.Fatal(err)
	}
	indexBody, err := json.Marshal(scanOCIDocument{Manifests: []scanOCIDescriptor{{Digest: childDigest, Size: int64(len(childBody)), MediaType: "application/vnd.oci.image.manifest.v1+json"}}})
	if err != nil {
		t.Fatal(err)
	}
	indexDigest := putScanFixtureObject(t, objects, "scan/oci-index", string(indexBody))
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: "oci", Name: "team/widget", Digest: indexDigest, ObjectKey: "scan/oci-index", MediaType: "application/vnd.oci.image.index.v1+json", Size: int64(len(indexBody))}, "latest"); err != nil {
		t.Fatal(err)
	}

	artifact, err := resolver.ResolveArtifactScan(ctx, "oci", repository.ArtifactScanPayload{Format: repository.FormatOCI, Coordinate: "team/widget", Digest: indexDigest})
	if err != nil {
		t.Fatal(err)
	}
	assertScanAssetsReadable(t, ctx, artifact.Assets, 4)

	missingDigest := testScanDigest("missing blob")
	brokenBody, err := json.Marshal(scanOCIDocument{Layers: []scanOCIDescriptor{{Digest: missingDigest, Size: 12}}})
	if err != nil {
		t.Fatal(err)
	}
	brokenDigest := putScanFixtureObject(t, objects, "scan/oci-broken", string(brokenBody))
	if _, err := store.PutOCIManifest(ctx, repository.OCIManifest{RepositoryID: "oci", Name: "team/broken", Digest: brokenDigest, ObjectKey: "scan/oci-broken", MediaType: "application/vnd.oci.image.manifest.v1+json", Size: int64(len(brokenBody))}, "latest"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveArtifactScan(ctx, "oci", repository.ArtifactScanPayload{Format: repository.FormatOCI, Coordinate: "team/broken", Digest: brokenDigest}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("missing OCI blob error=%v", err)
	}
}

func TestNativeArtifactScanResolverResolvesGoModuleAssets(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	if _, err := store.PutGoModuleVersion(ctx, repository.GoModuleVersion{RepositoryID: "go", Module: "example.com/team/widget", Version: "v1.2.3"}); err != nil {
		t.Fatal(err)
	}
	var zipDigest string
	for _, fixture := range []struct{ kind, body string }{{"info", `{"Version":"v1.2.3"}`}, {"mod", "module example.com/team/widget"}, {"zip", "zip bytes"}} {
		key := "scan/go-" + fixture.kind
		digest := putScanFixtureObject(t, objects, key, fixture.body)
		if fixture.kind == "zip" {
			zipDigest = digest
		}
		if _, err := store.CacheGoModuleAsset(ctx, repository.GoModuleAsset{RepositoryID: "go", Module: "example.com/team/widget", Version: "v1.2.3", Kind: fixture.kind, Digest: digest, ObjectKey: key, Size: int64(len(fixture.body))}); err != nil {
			t.Fatal(err)
		}
	}
	resolver := NewNativeArtifactScanResolver(store, objects)
	artifact, err := resolver.ResolveArtifactScan(ctx, "go", repository.ArtifactScanPayload{Format: repository.FormatGo, Coordinate: "example.com/team/widget@v1.2.3", Digest: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	assertScanAssetsReadable(t, ctx, artifact.Assets, 3)
	if _, err := resolver.ResolveArtifactScan(ctx, "go", repository.ArtifactScanPayload{Format: repository.FormatGo, Coordinate: "example.com/team/widget@v1.2.3", Digest: testScanDigest("unknown")}); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("unknown Go digest error=%v", err)
	}
}

func TestNativeArtifactScanResolverResolvesVisibleConanRevisions(t *testing.T) {
	ctx := context.Background()
	store := repository.NewMemoryStore()
	objects := NewMemoryOCIObjectStore()
	reference, recipeRevision := "widget/1.2.3@team/stable", "rrev"
	recipeDigest := testScanDigest("recipe revision")
	recipeAssets := []repository.ConanAsset{
		newScanConanAsset(t, objects, "conan", reference, recipeRevision, "", "", "conanfile.py", "recipe source"),
		newScanConanAsset(t, objects, "conan", reference, recipeRevision, "", "", "conanmanifest.txt", "recipe manifest"),
	}
	for _, asset := range recipeAssets {
		if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: "conan", ObjectKey: asset.ObjectKey, Digest: asset.Digest, Size: asset.Size}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.PutConanRecipeRevision(ctx, repository.ConanRecipeRevision{RepositoryID: "conan", Reference: reference, Revision: recipeRevision, Digest: recipeDigest}, recipeAssets); err != nil {
		t.Fatal(err)
	}
	packageDigest := testScanDigest("package revision")
	packageAsset := newScanConanAsset(t, objects, "conan", reference, recipeRevision, "package-id", "prev", "package.tgz", "package bytes")
	if err := store.StageConanObject(ctx, repository.ConanObjectIntent{RepositoryID: "conan", ObjectKey: packageAsset.ObjectKey, Digest: packageAsset.Digest, Size: packageAsset.Size}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutConanPackageRevision(ctx, repository.ConanPackageRevision{RepositoryID: "conan", Reference: reference, RecipeRevision: recipeRevision, PackageID: "package-id", Revision: "prev", Digest: packageDigest}, []repository.ConanAsset{packageAsset}); err != nil {
		t.Fatal(err)
	}
	resolver := NewNativeArtifactScanResolver(store, objects)

	recipe, err := resolver.ResolveArtifactScan(ctx, "conan", repository.ArtifactScanPayload{Format: repository.FormatConan, Coordinate: reference + "#" + recipeRevision, Digest: recipeDigest})
	if err != nil {
		t.Fatal(err)
	}
	assertScanAssetsReadable(t, ctx, recipe.Assets, 2)
	packageCoordinate := reference + "#" + recipeRevision + "/package-id#prev"
	pkg, err := resolver.ResolveArtifactScan(ctx, "conan", repository.ArtifactScanPayload{Format: repository.FormatConan, Coordinate: packageCoordinate, Digest: packageDigest})
	if err != nil {
		t.Fatal(err)
	}
	assertScanAssetsReadable(t, ctx, pkg.Assets, 1)
	if _, err := store.TombstoneConanPackageRevision(ctx, "conan", reference, recipeRevision, "package-id", "prev"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveArtifactScan(ctx, "conan", repository.ArtifactScanPayload{Format: repository.FormatConan, Coordinate: packageCoordinate, Digest: packageDigest}); err == nil {
		t.Fatal("ResolveArtifactScan() accepted a tombstoned Conan package")
	}
}

func putScanFixtureObject(t *testing.T, objects *MemoryOCIObjectStore, key, body string) string {
	t.Helper()
	if err := objects.Put(context.Background(), key, []byte(body)); err != nil {
		t.Fatal(err)
	}
	return testScanDigest(body)
}

func registerScanOCIBlob(t *testing.T, ctx context.Context, store *repository.MemoryStore, repositoryID, id, key, digest string, size int64) {
	t.Helper()
	if _, err := store.CreateOCIUpload(ctx, repository.OCIUpload{ID: "scan-" + id, RepositoryID: repositoryID, State: "open", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteOCIUpload(ctx, "scan-"+id, repository.OCIBlob{Digest: digest, ObjectKey: key, Size: size}); err != nil {
		t.Fatal(err)
	}
}

func newScanConanAsset(t *testing.T, objects *MemoryOCIObjectStore, repositoryID, reference, recipeRevision, packageID, packageRevision, path, body string) repository.ConanAsset {
	t.Helper()
	key := "scan/conan/" + path
	digest := putScanFixtureObject(t, objects, key, body)
	return repository.ConanAsset{RepositoryID: repositoryID, Reference: reference, RecipeRevision: recipeRevision, PackageID: packageID, PackageRevision: packageRevision, Path: path, ObjectKey: key, Digest: digest, Size: int64(len(body))}
}

func assertScanAssetsReadable(t *testing.T, ctx context.Context, assets []scanning.Asset, want int) {
	t.Helper()
	if len(assets) != want {
		t.Fatalf("asset count=%d want=%d", len(assets), want)
	}
	for _, asset := range assets {
		reader, err := asset.Open(ctx)
		if err != nil {
			t.Fatalf("open %q: %v", asset.Path, err)
		}
		body, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read %q: read=%v close=%v", asset.Path, readErr, closeErr)
		}
		if digest := testScanDigest(string(body)); digest != asset.Digest {
			t.Fatalf("asset %q digest=%q want=%q", asset.Path, asset.Digest, digest)
		}
	}
}
