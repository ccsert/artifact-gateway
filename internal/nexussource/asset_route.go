package nexussource

import (
	"strings"

	conanprotocol "github.com/ccsert/artifact-gateway/internal/protocol/conan"
	npmprotocol "github.com/ccsert/artifact-gateway/internal/protocol/npm"
	pypi "github.com/ccsert/artifact-gateway/internal/protocol/pypi"
	"path"
)

// NativeFormat translates the source's format identity into the target domain.
func NativeFormat(source string) (string, bool) {
	switch source {
	case "maven2":
		return "maven", true
	case "docker":
		return "oci", true
	case "raw", "conan", "npm", "pypi":
		return source, true
	default:
		return "", false
	}
}

// assetRoute separates a source storage identity from its protocol download URL.
// Unsupported safe shapes may be reported, but OpenAsset never fetches them.
func assetRoute(format, value string) (canonical, wire, reason string) {
	canonical = value
	if format == "oci" {
		canonical = strings.TrimPrefix(value, "/")
	}
	if !validAssetPath(canonical) {
		return "", "", "source_path_unsupported"
	}
	wire = canonical
	switch format {
	case "raw", "maven":
		return canonical, wire, ""
	case "oci":
		if normalized, route, why := OCISourcePath(value); why == "" {
			return normalized, strings.TrimPrefix(route, "/"), ""
		}
		return canonical, wire, "oci_source_path_unsupported"
	case "pypi":
		if _, err := pypi.FilenameIdentity(path.Base(canonical)); err == nil {
			return canonical, wire, ""
		}
		return canonical, wire, "pypi_source_shape_unsupported"
	case "npm":
		if npmprotocol.ValidPackageName(canonical) {
			return canonical, wire, ""
		}
		if name, tarball, ok := strings.Cut(canonical, "/-/"); ok && npmprotocol.ValidPackageName(name) && npmprotocol.ValidTarballName(tarball) {
			return canonical, wire, ""
		}
		return canonical, wire, "npm_source_shape_unsupported"
	case "conan":
		if strings.HasPrefix(canonical, "conans/") {
			wire = "v2/" + canonical
			if _, err := conanprotocol.ParseUploadPath("/conan/migration/" + wire); err == nil {
				return canonical, wire, ""
			}
		}
		return canonical, wire, "conan_source_shape_unsupported"
	default:
		return canonical, wire, "format_unsupported"
	}
}
