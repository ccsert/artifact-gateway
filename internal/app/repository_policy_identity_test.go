package app

import (
	"context"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

type artifactIdentityProjectionScope struct {
	repositoryID string
	format       repository.Format
}

type artifactIdentityProjectionQuery struct {
	repositoryID string
	format       repository.Format
	query        repository.ArtifactSearchQuery
}

type artifactIdentityProjectionStore struct {
	repository.NativeConanStore
	items    map[artifactIdentityProjectionScope][]repository.ArtifactSearchItem
	recipes  map[string]repository.ConanRecipeRevision
	packages map[string]repository.ConanPackageRevision
	queries  []artifactIdentityProjectionQuery
}

type nativeNPMArtifactIdentityStore struct {
	*artifactIdentityProjectionStore
	repository.NativeNPMStore
	versions map[string]repository.NPMVersion
}

func (s *nativeNPMArtifactIdentityStore) GetNPMVersion(_ context.Context, repositoryID, packageName, version string) (repository.NPMVersion, error) {
	item, ok := s.versions[strings.Join([]string{repositoryID, packageName, version}, "\x00")]
	if !ok {
		return repository.NPMVersion{}, repository.ErrNotFound
	}
	return item, nil
}

type nativePyPIArtifactIdentityStore struct {
	*artifactIdentityProjectionStore
	repository.NativePyPIStore
	files map[string][]repository.PyPIFile
}

func (s *nativePyPIArtifactIdentityStore) ListPyPIProjectFiles(_ context.Context, repositoryID, project string) ([]repository.PyPIFile, error) {
	items := s.files[strings.Join([]string{repositoryID, project}, "\x00")]
	if len(items) == 0 {
		return nil, repository.ErrNotFound
	}
	return append([]repository.PyPIFile(nil), items...), nil
}

type nativeGoArtifactIdentityStore struct {
	*artifactIdentityProjectionStore
	repository.NativeGoStore
	versions map[string]repository.GoModuleVersion
	assets   map[string]repository.GoModuleAsset
}

func (s *nativeGoArtifactIdentityStore) GetGoModuleVersion(_ context.Context, repositoryID, module, version string) (repository.GoModuleVersion, error) {
	item, ok := s.versions[strings.Join([]string{repositoryID, module, version}, "\x00")]
	if !ok {
		return repository.GoModuleVersion{}, repository.ErrNotFound
	}
	return item, nil
}

func (s *nativeGoArtifactIdentityStore) GetGoModuleAsset(_ context.Context, repositoryID, module, version, kind string) (repository.GoModuleAsset, error) {
	item, ok := s.assets[strings.Join([]string{repositoryID, module, version, kind}, "\x00")]
	if !ok {
		return repository.GoModuleAsset{}, repository.ErrNotFound
	}
	return item, nil
}

func (s *artifactIdentityProjectionStore) SearchArtifactProjection(_ context.Context, repositoryID string, format repository.Format, query repository.ArtifactSearchQuery, _ int, _ repository.ArtifactSearchPosition) ([]repository.ArtifactSearchItem, error) {
	s.queries = append(s.queries, artifactIdentityProjectionQuery{repositoryID: repositoryID, format: format, query: query})
	return append([]repository.ArtifactSearchItem(nil), s.items[artifactIdentityProjectionScope{repositoryID: repositoryID, format: format}]...), nil
}

func (s *artifactIdentityProjectionStore) GetConanRecipeRevision(_ context.Context, repositoryID, reference, revision string) (repository.ConanRecipeRevision, error) {
	item, ok := s.recipes[strings.Join([]string{repositoryID, reference, revision}, "\x00")]
	if !ok {
		return repository.ConanRecipeRevision{}, repository.ErrNotFound
	}
	return item, nil
}

func (s *artifactIdentityProjectionStore) GetConanPackageRevision(_ context.Context, repositoryID, reference, recipeRevision, packageID, revision string) (repository.ConanPackageRevision, error) {
	item, ok := s.packages[strings.Join([]string{repositoryID, reference, recipeRevision, packageID, revision}, "\x00")]
	if !ok {
		return repository.ConanPackageRevision{}, repository.ErrNotFound
	}
	return item, nil
}

func TestArtifactSearchItemMatchesCanonicalIdentity(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	otherDigest := "sha256:" + strings.Repeat("b", 64)
	tests := []struct {
		name       string
		format     repository.Format
		item       repository.ArtifactSearchItem
		coordinate string
		digest     string
		want       bool
	}{
		{name: "maven", format: repository.FormatMaven, item: repository.ArtifactSearchItem{Coordinate: "com.acme:widget:1.2.3", Version: "1.2.3", Digest: digest}, coordinate: "com.acme:widget:1.2.3", digest: digest, want: true},
		{name: "oci", format: repository.FormatOCI, item: repository.ArtifactSearchItem{Coordinate: "team/widget:stable", Digest: digest}, coordinate: "team/widget:stable", digest: digest, want: true},
		{name: "raw", format: repository.FormatRaw, item: repository.ArtifactSearchItem{Coordinate: "dist/widget.tar.gz", Digest: digest}, coordinate: "dist/widget.tar.gz", digest: digest, want: true},
		{name: "npm", format: repository.FormatNPM, item: repository.ArtifactSearchItem{Coordinate: "@scope/widget", Version: "1.2.3", Digest: digest}, coordinate: "@scope/widget@1.2.3", digest: digest, want: true},
		{name: "pypi", format: repository.FormatPyPI, item: repository.ArtifactSearchItem{Coordinate: "my-widget", Version: "1.2.3", Digest: digest}, coordinate: "my-widget@1.2.3", digest: digest, want: true},
		{name: "go", format: repository.FormatGo, item: repository.ArtifactSearchItem{Coordinate: "example.test/widget/v2", Version: "v2.3.4", Digest: digest}, coordinate: "example.test/widget/v2@v2.3.4", digest: digest, want: true},
		{name: "apt", format: repository.FormatAPT, item: repository.ArtifactSearchItem{Coordinate: "pool/main/w/widget.deb", Digest: digest}, coordinate: "pool/main/w/widget.deb", digest: digest, want: true},
		{name: "coordinate mismatch", format: repository.FormatNPM, item: repository.ArtifactSearchItem{Coordinate: "@scope/widget", Version: "1.2.3", Digest: digest}, coordinate: "@scope/other@1.2.3", digest: digest},
		{name: "version mismatch", format: repository.FormatPyPI, item: repository.ArtifactSearchItem{Coordinate: "my-widget", Version: "1.2.3", Digest: digest}, coordinate: "my-widget@2.0.0", digest: digest},
		{name: "digest mismatch", format: repository.FormatGo, item: repository.ArtifactSearchItem{Coordinate: "example.test/widget", Version: "v1.0.0", Digest: digest}, coordinate: "example.test/widget@v1.0.0", digest: otherDigest},
		{name: "npm projection is not raw", format: repository.FormatRaw, item: repository.ArtifactSearchItem{Coordinate: "@scope/widget", Version: "1.2.3", Digest: digest}, coordinate: "@scope/widget@1.2.3", digest: digest},
		{name: "raw projection is not npm", format: repository.FormatNPM, item: repository.ArtifactSearchItem{Coordinate: "dist/widget@1.2.3", Digest: digest}, coordinate: "dist/widget@1.2.3", digest: digest},
		{name: "conan requires native revision lookup", format: repository.FormatConan, item: repository.ArtifactSearchItem{Coordinate: "widget/1.2.3/acme/stable", Version: "1.2.3", Digest: digest}, coordinate: "widget/1.2.3/acme/stable#rrev", digest: digest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := artifactSearchItemMatchesCanonicalIdentity(test.format, test.item, test.coordinate, test.digest); got != test.want {
				t.Fatalf("artifactSearchItemMatchesCanonicalIdentity()=%t, want %t", got, test.want)
			}
		})
	}
}

func TestSecurityPolicyArtifactVisibleUsesExactRepositoryAndFormatScope(t *testing.T) {
	digest := "sha256:" + strings.Repeat("c", 64)
	item := repository.ArtifactSearchItem{Coordinate: "@scope/widget", Version: "1.2.3", Digest: digest}
	tests := []struct {
		name         string
		repositoryID string
		format       repository.Format
		want         bool
	}{
		{name: "exact scope", repositoryID: "repository", format: repository.FormatNPM, want: true},
		{name: "different repository", repositoryID: "other-repository", format: repository.FormatNPM},
		{name: "different format", repositoryID: "repository", format: repository.FormatPyPI},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &artifactIdentityProjectionStore{items: map[artifactIdentityProjectionScope][]repository.ArtifactSearchItem{
				{repositoryID: "repository", format: repository.FormatNPM}: {item},
			}}
			visible, err := securityPolicyArtifactVisible(context.Background(), store, test.repositoryID, test.format, "@scope/widget@1.2.3", digest)
			if err != nil {
				t.Fatal(err)
			}
			if visible != test.want {
				t.Fatalf("visible=%t, want %t", visible, test.want)
			}
			if len(store.queries) != 1 {
				t.Fatalf("queries=%d, want 1", len(store.queries))
			}
			query := store.queries[0]
			if query.repositoryID != test.repositoryID || query.format != test.format || query.query.Mode != repository.ArtifactSearchByDigest || query.query.Value != digest {
				t.Fatalf("unexpected projection query: %+v", query)
			}
		})
	}
}

func TestSecurityPolicyArtifactVisibleUsesNativeVersionIdentityWhenProjectionCollapsesSameDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("f", 64)
	repositoryID := "repository"
	tests := []struct {
		name       string
		format     repository.Format
		coordinate string
		store      repository.ArtifactSearchStore
	}{
		{
			name: "npm", format: repository.FormatNPM, coordinate: "@scope/widget@1.0.0",
			store: &nativeNPMArtifactIdentityStore{
				artifactIdentityProjectionStore: &artifactIdentityProjectionStore{items: map[artifactIdentityProjectionScope][]repository.ArtifactSearchItem{
					{repositoryID: repositoryID, format: repository.FormatNPM}: {{Coordinate: "@scope/widget", Version: "2.0.0", Digest: digest}},
				}},
				versions: map[string]repository.NPMVersion{
					strings.Join([]string{repositoryID, "@scope/widget", "1.0.0"}, "\x00"): {RepositoryID: repositoryID, PackageName: "@scope/widget", Version: "1.0.0", Digest: digest, State: "visible"},
					strings.Join([]string{repositoryID, "@scope/widget", "2.0.0"}, "\x00"): {RepositoryID: repositoryID, PackageName: "@scope/widget", Version: "2.0.0", Digest: digest, State: "visible"},
				},
			},
		},
		{
			name: "pypi", format: repository.FormatPyPI, coordinate: "widget@1.0.0",
			store: &nativePyPIArtifactIdentityStore{
				artifactIdentityProjectionStore: &artifactIdentityProjectionStore{items: map[artifactIdentityProjectionScope][]repository.ArtifactSearchItem{
					{repositoryID: repositoryID, format: repository.FormatPyPI}: {{Coordinate: "widget", Version: "2.0.0", Digest: digest}},
				}},
				files: map[string][]repository.PyPIFile{
					strings.Join([]string{repositoryID, "widget"}, "\x00"): {
						{RepositoryID: repositoryID, Project: "widget", Version: "1.0.0", Digest: digest, State: "visible"},
						{RepositoryID: repositoryID, Project: "widget", Version: "2.0.0", Digest: digest, State: "visible"},
					},
				},
			},
		},
		{
			name: "go", format: repository.FormatGo, coordinate: "example.test/widget@v1.0.0",
			store: &nativeGoArtifactIdentityStore{
				artifactIdentityProjectionStore: &artifactIdentityProjectionStore{items: map[artifactIdentityProjectionScope][]repository.ArtifactSearchItem{
					{repositoryID: repositoryID, format: repository.FormatGo}: {{Coordinate: "example.test/widget", Version: "v2.0.0", Digest: digest}},
				}},
				versions: map[string]repository.GoModuleVersion{
					strings.Join([]string{repositoryID, "example.test/widget", "v1.0.0"}, "\x00"): {RepositoryID: repositoryID, Module: "example.test/widget", Version: "v1.0.0"},
					strings.Join([]string{repositoryID, "example.test/widget", "v2.0.0"}, "\x00"): {RepositoryID: repositoryID, Module: "example.test/widget", Version: "v2.0.0"},
				},
				assets: map[string]repository.GoModuleAsset{
					strings.Join([]string{repositoryID, "example.test/widget", "v1.0.0", "zip"}, "\x00"): {RepositoryID: repositoryID, Module: "example.test/widget", Version: "v1.0.0", Kind: "zip", Digest: digest},
					strings.Join([]string{repositoryID, "example.test/widget", "v2.0.0", "zip"}, "\x00"): {RepositoryID: repositoryID, Module: "example.test/widget", Version: "v2.0.0", Kind: "zip", Digest: digest},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			visible, err := securityPolicyArtifactVisible(context.Background(), test.store, repositoryID, test.format, test.coordinate, digest)
			if err != nil || !visible {
				t.Fatalf("visible=%t err=%v", visible, err)
			}
		})
	}
}

func TestSecurityPolicyArtifactVisibleDoesNotTrustStaleVersionProjectionOverNativeStore(t *testing.T) {
	digest := "sha256:" + strings.Repeat("9", 64)
	repositoryID := "repository"
	tests := []struct {
		name       string
		format     repository.Format
		coordinate string
		store      repository.ArtifactSearchStore
	}{
		{
			name: "npm", format: repository.FormatNPM, coordinate: "@scope/widget@1.0.0",
			store: &nativeNPMArtifactIdentityStore{
				artifactIdentityProjectionStore: &artifactIdentityProjectionStore{items: map[artifactIdentityProjectionScope][]repository.ArtifactSearchItem{
					{repositoryID: repositoryID, format: repository.FormatNPM}: {{Coordinate: "@scope/widget", Version: "1.0.0", Digest: digest}},
				}},
				versions: map[string]repository.NPMVersion{
					strings.Join([]string{repositoryID, "@scope/widget", "2.0.0"}, "\x00"): {RepositoryID: repositoryID, PackageName: "@scope/widget", Version: "2.0.0", Digest: digest, State: "visible"},
				},
			},
		},
		{
			name: "pypi", format: repository.FormatPyPI, coordinate: "widget@1.0.0",
			store: &nativePyPIArtifactIdentityStore{
				artifactIdentityProjectionStore: &artifactIdentityProjectionStore{items: map[artifactIdentityProjectionScope][]repository.ArtifactSearchItem{
					{repositoryID: repositoryID, format: repository.FormatPyPI}: {{Coordinate: "widget", Version: "1.0.0", Digest: digest}},
				}},
				files: map[string][]repository.PyPIFile{
					strings.Join([]string{repositoryID, "widget"}, "\x00"): {{RepositoryID: repositoryID, Project: "widget", Version: "2.0.0", Digest: digest, State: "visible"}},
				},
			},
		},
		{
			name: "go", format: repository.FormatGo, coordinate: "example.test/widget@v1.0.0",
			store: &nativeGoArtifactIdentityStore{
				artifactIdentityProjectionStore: &artifactIdentityProjectionStore{items: map[artifactIdentityProjectionScope][]repository.ArtifactSearchItem{
					{repositoryID: repositoryID, format: repository.FormatGo}: {{Coordinate: "example.test/widget", Version: "v1.0.0", Digest: digest}},
				}},
				versions: map[string]repository.GoModuleVersion{
					strings.Join([]string{repositoryID, "example.test/widget", "v2.0.0"}, "\x00"): {RepositoryID: repositoryID, Module: "example.test/widget", Version: "v2.0.0"},
				},
				assets: map[string]repository.GoModuleAsset{
					strings.Join([]string{repositoryID, "example.test/widget", "v2.0.0", "zip"}, "\x00"): {RepositoryID: repositoryID, Module: "example.test/widget", Version: "v2.0.0", Kind: "zip", Digest: digest},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			visible, err := securityPolicyArtifactVisible(context.Background(), test.store, repositoryID, test.format, test.coordinate, digest)
			if err != nil {
				t.Fatal(err)
			}
			if visible {
				t.Fatal("stale projection made a missing native version visible")
			}
		})
	}
}

func TestSecurityPolicyArtifactVisibleMatchesExactConanRevision(t *testing.T) {
	digest := "sha256:" + strings.Repeat("d", 64)
	otherDigest := "sha256:" + strings.Repeat("e", 64)
	reference := "widget/1.2.3/acme/stable"
	recipeKey := strings.Join([]string{"repository", reference, "rrev"}, "\x00")
	packageKey := strings.Join([]string{"repository", reference, "rrev", "package-id", "prev"}, "\x00")
	tests := []struct {
		name       string
		coordinate string
		digest     string
		recipes    map[string]repository.ConanRecipeRevision
		packages   map[string]repository.ConanPackageRevision
		want       bool
	}{
		{name: "recipe", coordinate: reference + "#rrev", digest: digest, recipes: map[string]repository.ConanRecipeRevision{recipeKey: {State: "visible", Digest: digest}}, want: true},
		{name: "package", coordinate: reference + "#rrev/package-id#prev", digest: digest, packages: map[string]repository.ConanPackageRevision{packageKey: {State: "visible", Digest: digest}}, want: true},
		{name: "missing recipe revision with same projected reference and digest", coordinate: reference + "#missing", digest: digest, recipes: map[string]repository.ConanRecipeRevision{recipeKey: {State: "visible", Digest: digest}}},
		{name: "recipe in different repository", coordinate: reference + "#rrev", digest: digest, recipes: map[string]repository.ConanRecipeRevision{strings.Join([]string{"other-repository", reference, "rrev"}, "\x00"): {State: "visible", Digest: digest}}},
		{name: "recipe digest mismatch", coordinate: reference + "#rrev", digest: otherDigest, recipes: map[string]repository.ConanRecipeRevision{recipeKey: {State: "visible", Digest: digest}}},
		{name: "tombstoned recipe", coordinate: reference + "#rrev", digest: digest, recipes: map[string]repository.ConanRecipeRevision{recipeKey: {State: "tombstoned", Digest: digest}}},
		{name: "missing package revision", coordinate: reference + "#rrev/package-id#missing", digest: digest, packages: map[string]repository.ConanPackageRevision{packageKey: {State: "visible", Digest: digest}}},
		{name: "package digest mismatch", coordinate: reference + "#rrev/package-id#prev", digest: otherDigest, packages: map[string]repository.ConanPackageRevision{packageKey: {State: "visible", Digest: digest}}},
		{name: "tombstoned package", coordinate: reference + "#rrev/package-id#prev", digest: digest, packages: map[string]repository.ConanPackageRevision{packageKey: {State: "tombstoned", Digest: digest}}},
		{name: "malformed coordinate", coordinate: reference, digest: digest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &artifactIdentityProjectionStore{
				items: map[artifactIdentityProjectionScope][]repository.ArtifactSearchItem{
					{repositoryID: "repository", format: repository.FormatConan}: {{Coordinate: reference, Version: "1.2.3", Digest: digest}},
				},
				recipes: test.recipes, packages: test.packages,
			}
			visible, err := securityPolicyArtifactVisible(context.Background(), store, "repository", repository.FormatConan, test.coordinate, test.digest)
			if err != nil {
				t.Fatal(err)
			}
			if visible != test.want {
				t.Fatalf("visible=%t, want %t", visible, test.want)
			}
			if len(store.queries) != 0 {
				t.Fatalf("Conan visibility used inexact search projection")
			}
		})
	}
}
