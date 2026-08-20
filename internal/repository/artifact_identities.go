package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	protocolidentity "github.com/artifact-gateway/artifact-gateway/internal/protocol/identity"
)

func (s *MemoryStore) ListArtifactIdentities(_ context.Context, repositoryID string, format Format, purpose ArtifactIdentityPurpose, query string, limit int) ([]ArtifactIdentity, error) {
	if purpose != ArtifactIdentityScan && purpose != ArtifactIdentityDistribution {
		return nil, fmt.Errorf("unsupported artifact identity purpose %q", purpose)
	}
	limit = artifactIdentityLimit(limit)
	query = strings.ToLower(strings.TrimSpace(query))

	s.mu.RLock()
	defer s.mu.RUnlock()
	identities, err := s.artifactIdentitiesLocked(repositoryID, format, purpose, query)
	if err != nil {
		return nil, err
	}
	sort.Slice(identities, func(i, j int) bool {
		if !identities[i].PublishedAt.Equal(identities[j].PublishedAt) {
			return identities[i].PublishedAt.After(identities[j].PublishedAt)
		}
		if identities[i].Coordinate != identities[j].Coordinate {
			return identities[i].Coordinate < identities[j].Coordinate
		}
		return identities[i].Digest > identities[j].Digest
	})
	if len(identities) > limit {
		identities = identities[:limit]
	}
	return identities, nil
}

// artifactIdentitiesLocked is the single in-memory projection for protocol-owned
// immutable identities. Callers must hold s.mu for reading.
func (s *MemoryStore) artifactIdentitiesLocked(repositoryID string, format Format, purpose ArtifactIdentityPurpose, query string) ([]ArtifactIdentity, error) {
	identities := make([]ArtifactIdentity, 0, 64)
	identityIndexes := make(map[string]int, 64)
	appendIdentity := func(coordinate, digest string, size *int64, publishedAt time.Time) {
		if coordinate == "" || !protocolidentity.IsSHA256Digest(digest) || query != "" && !strings.Contains(strings.ToLower(coordinate), query) && strings.ToLower(digest) != query {
			return
		}
		identity := ArtifactIdentity{Coordinate: coordinate, Digest: digest, Size: size, PublishedAt: publishedAt}
		if value, ok := s.artifactIntelligence[artifactIntelligenceKey(repositoryID, format, coordinate, digest)]; ok {
			identity.Intelligence = summarizeArtifactIntelligence(value)
		}
		key := coordinate + "\x00" + digest
		if index, exists := identityIndexes[key]; exists {
			if identity.PublishedAt.After(identities[index].PublishedAt) {
				identities[index] = identity
			}
			return
		}
		identityIndexes[key] = len(identities)
		identities = append(identities, identity)
	}

	sizeOf := func(value int64) *int64 {
		copy := value
		return &copy
	}
	switch format {
	case FormatMaven:
		for _, artifact := range s.mavenArtifacts {
			if artifact.RepositoryID == repositoryID && artifact.State == "visible" {
				appendIdentity(protocolidentity.Maven(artifact.Coordinate), artifact.Digest, nil, artifact.CreatedAt)
			}
		}
	case FormatOCI:
		for _, manifest := range s.ociManifests {
			if manifest.RepositoryID == repositoryID && manifest.ObjectKey != "" {
				appendIdentity(protocolidentity.OCI(manifest.Name), manifest.Digest, sizeOf(manifest.Size), manifest.CreatedAt)
			}
		}
	case FormatRaw:
		for _, asset := range s.rawAssets {
			if asset.RepositoryID == repositoryID && asset.ObjectKey != "" {
				appendIdentity(protocolidentity.Raw(asset.Path), asset.Digest, sizeOf(asset.Size), asset.UpdatedAt)
			}
		}
	case FormatNPM:
		for _, version := range s.npmVersions {
			pkg, packageExists := s.npmPackages[npmPackageKey(version.RepositoryID, version.PackageName)]
			if version.RepositoryID == repositoryID && version.State == "visible" && version.ObjectKey != "" && packageExists && !pkg.Negative {
				appendIdentity(protocolidentity.NPMVersion(version.PackageName, version.Version), version.Digest, sizeOf(version.Size), version.CreatedAt)
			}
		}
	case FormatPyPI:
		for _, file := range s.pypiFiles {
			if file.RepositoryID == repositoryID && file.State == "visible" && file.ObjectKey != "" {
				appendIdentity(protocolidentity.PyPIVersion(file.Project, file.Version), file.Digest, sizeOf(file.Size), file.CreatedAt)
			}
		}
	case FormatGo:
		for _, version := range s.goVersions {
			if version.RepositoryID != repositoryID || s.goModuleVersionTombstonedLocked(repositoryID, version.Module, version.Version) {
				continue
			}
			var zip GoModuleAsset
			complete := true
			for _, kind := range []string{"info", "mod", "zip"} {
				asset, ok := s.goAssets[goAssetKey(repositoryID, version.Module, version.Version, kind)]
				if !ok || asset.ObjectKey == "" || !asset.CollectingAt.IsZero() || !asset.CollectedAt.IsZero() {
					complete = false
					break
				}
				if kind == "zip" {
					zip = asset
				}
			}
			if complete {
				appendIdentity(protocolidentity.GoVersion(version.Module, version.Version), zip.Digest, sizeOf(zip.Size), version.CreatedAt)
			}
		}
	case FormatConan:
		for _, revision := range s.conanRecipes {
			resolvable := s.hasResolvableConanAssetLocked(repositoryID, revision.Reference, revision.Revision, "", "")
			if purpose == ArtifactIdentityDistribution {
				resolvable = resolvable && s.hasResolvableConanDistributionLocked(repositoryID, revision.Reference, revision.Revision)
			}
			if revision.RepositoryID == repositoryID && revision.State == "visible" && resolvable {
				appendIdentity(protocolidentity.ConanRecipe(revision.Reference, revision.Revision), revision.Digest, nil, revision.CreatedAt)
			}
		}
		if purpose == ArtifactIdentityScan {
			for _, revision := range s.conanPackages {
				recipe, recipeExists := s.conanRecipes[conanRecipeKey(repositoryID, revision.Reference, revision.RecipeRevision)]
				if revision.RepositoryID == repositoryID && revision.State == "visible" && recipeExists && recipe.State == "visible" && s.hasResolvableConanAssetLocked(repositoryID, revision.Reference, revision.RecipeRevision, revision.PackageID, revision.Revision) {
					appendIdentity(protocolidentity.ConanPackage(revision.Reference, revision.RecipeRevision, revision.PackageID, revision.Revision), revision.Digest, nil, revision.CreatedAt)
				}
			}
		}
	default:
		return nil, fmt.Errorf("format %q does not support artifact identities", format)
	}

	return identities, nil
}

func (s *MemoryStore) hasResolvableConanAssetLocked(repositoryID, reference, recipeRevision, packageID, packageRevision string) bool {
	for _, asset := range s.conanAssets {
		if asset.RepositoryID != repositoryID || asset.Reference != reference || asset.RecipeRevision != recipeRevision || asset.PackageID != packageID || asset.PackageRevision != packageRevision || asset.ObjectKey == "" {
			continue
		}
		object, exists := s.conanObjects[asset.ObjectKey]
		if exists && object.CollectedAt.IsZero() {
			return true
		}
	}
	return false
}

func (s *MemoryStore) hasResolvableConanDistributionLocked(repositoryID, reference, recipeRevision string) bool {
	for _, revision := range s.conanPackages {
		if revision.RepositoryID == repositoryID && revision.Reference == reference && revision.RecipeRevision == recipeRevision && revision.State == "visible" && !s.hasResolvableConanAssetLocked(repositoryID, reference, recipeRevision, revision.PackageID, revision.Revision) {
			return false
		}
	}
	return true
}

func artifactIdentityLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 200 {
		return 200
	}
	return limit
}
