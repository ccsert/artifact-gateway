package nexussource

import (
	"strings"

	"github.com/ccsert/artifact-gateway/internal/ocigraph"
)

// OCIAssetPath is the public Nexus Docker asset identity. The shared "-"
// namespace is admitted only for digest blobs, never as an image identity.
type OCIAssetPath struct{ Name, Kind, Reference string }

// OCISourcePath accepts the documented Assets API path, with at most one leading
// slash. canonical is stored without that slash; wire is the exact /v2/... suffix
// to append to /repository/<name>. Unknown layouts are explicitly unsupported.
func OCISourcePath(value string) (canonical, wire, reason string) {
	canonical = strings.TrimPrefix(value, "/")
	if !validAssetPath(canonical) {
		return "", "", "oci_source_path_unsupported"
	}
	if _, ok := ParseOCIAssetPath(canonical); !ok {
		return "", "", "oci_source_path_unsupported"
	}
	return canonical, "/" + canonical, ""
}

func ParseOCIAssetPath(value string) (OCIAssetPath, bool) {
	var out OCIAssetPath
	if !strings.HasPrefix(value, "v2/") || len(value) > 2048 {
		return out, false
	}
	value = strings.TrimPrefix(value, "v2/")
	for _, kind := range []string{"manifests", "blobs"} {
		separator := strings.LastIndex(value, "/"+kind+"/")
		if separator < 0 {
			continue
		}
		name, reference := value[:separator], value[separator+len(kind)+2:]
		if name != "-" && !ocigraph.ValidName(name) || strings.Contains(reference, "/") {
			continue
		}
		if kind == "blobs" {
			if !ocigraph.ValidDigest(reference) {
				return out, false
			}
		} else if name == "-" || !ocigraph.ValidReference(reference) {
			return out, false
		}
		return OCIAssetPath{Name: name, Kind: kind, Reference: reference}, true
	}
	return out, false
}

func ParseOCIIdentity(value string) (name, reference string, ok bool) {
	if name, reference, ok = strings.Cut(value, "@"); ok {
		return name, reference, ocigraph.ValidName(name) && ocigraph.ValidDigest(reference)
	}
	name, reference, ok = strings.Cut(value, ":")
	return name, reference, ok && ocigraph.ValidName(name) && !ocigraph.ValidDigest(reference) && ocigraph.ValidReference(reference)
}
