package nexusmigration

import (
	"strings"

	conanprotocol "github.com/ccsert/artifact-gateway/internal/protocol/conan"
)

// ConanCoordinate is the immutable identity of one complete Conan 2 revision.
// It is derived from the source's native asset path, never a Browse projection.
type ConanCoordinate struct {
	Kind, Reference, RecipeRevision, PackageID, PackageRevision string
}

func conanCoordinate(p conanprotocol.UploadPath) ConanCoordinate {
	return ConanCoordinate{p.Kind(), p.Reference, p.RecipeRevision, p.PackageID, p.PackageRevision}
}

func (c ConanCoordinate) Identity() string {
	identity := c.Reference + "#" + c.RecipeRevision
	if c.Kind == "package" {
		identity += "/" + c.PackageID + "#" + c.PackageRevision
	}
	return identity
}

// ConanAssetIdentity admits the canonical Nexus Conan 2 storage path. Unknown
// shapes, mutable/SCM revisions and optional metadata are explicitly unsupported.
func ConanAssetIdentity(sourcePath string) (identity, file string, err error) {
	if !strings.HasPrefix(sourcePath, "conans/") {
		return "", "", Failed("conan_source_shape_unsupported")
	}
	p, parseErr := conanprotocol.ParseUploadPath("/conan/migration/v2/" + sourcePath)
	if parseErr != nil {
		return "", "", Failed("conan_source_shape_unsupported")
	}
	return conanCoordinate(p).Identity(), p.File, nil
}

func ParseConanIdentity(identity string) (ConanCoordinate, error) {
	parts := strings.Split(identity, "#")
	if len(parts) < 2 || len(parts) > 3 {
		return ConanCoordinate{}, Failed("conan_identity_invalid")
	}
	path := "/conan/migration/v2/conans/" + parts[0] + "/revisions/"
	if len(parts) == 2 {
		path += parts[1] + "/files/conanmanifest.txt"
	} else {
		revisionAndPackage := strings.Split(parts[1], "/")
		if len(revisionAndPackage) != 2 {
			return ConanCoordinate{}, Failed("conan_identity_invalid")
		}
		path += revisionAndPackage[0] + "/packages/" + revisionAndPackage[1] + "/revisions/" + parts[2] + "/files/conanmanifest.txt"
	}
	p, err := conanprotocol.ParseUploadPath(path)
	if err != nil || conanCoordinate(p).Identity() != identity {
		return ConanCoordinate{}, Failed("conan_identity_invalid")
	}
	return conanCoordinate(p), nil
}

// ValidateConanEntry checks the fixed inventory closure before any target write.
// Manifest contents and uncompressed tree hashes are revalidated from staged
// bytes at the native publication boundary.
func ValidateConanEntry(entry Entry) error {
	c, err := ParseConanIdentity(entry.Identity)
	if err != nil || entry.Format != "conan" || len(entry.Assets) < 2 || len(entry.Assets) > 4 {
		return Failed("conan_revision_incomplete")
	}
	seen := make(map[string]bool, len(entry.Assets))
	for _, asset := range entry.Assets {
		identity, name, err := ConanAssetIdentity(asset.SourcePath)
		if err != nil || identity != entry.Identity || asset.TargetPath != name || seen[name] ||
			asset.Size < 0 || asset.Size > 1<<30 || len(asset.SHA256) != 64 || strings.Trim(asset.SHA256, "0123456789abcdef") != "" {
			return Failed("conan_revision_incomplete")
		}
		if (name == conanprotocol.ManifestName || name == "conaninfo.txt") && asset.Size > conanprotocol.ManifestMaxBytes {
			return Failed("conan_revision_incomplete")
		}
		seen[name] = true
	}
	if !seen[conanprotocol.ManifestName] || c.Kind == "recipe" && !seen["conanfile.py"] && !seen["conan_export.tgz"] ||
		c.Kind == "package" && (!seen["conaninfo.txt"] || !seen["conan_package.tgz"]) {
		return Failed("conan_revision_incomplete")
	}
	return nil
}
