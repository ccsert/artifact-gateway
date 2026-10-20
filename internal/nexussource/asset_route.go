package nexussource

import (
	"strings"

	conanprotocol "github.com/ccsert/artifact-gateway/internal/protocol/conan"
)

// NativeFormat translates the source's format identity into the target domain.
func NativeFormat(source string) (string, bool) {
	switch source {
	case "maven2":
		return "maven", true
	case "docker":
		return "oci", true
	case "raw", "conan":
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
