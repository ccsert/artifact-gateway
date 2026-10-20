package nexussource

import (
	"strings"
	"testing"
)

func TestOCISourcePublicAssetPaths(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, path := range []string{"v2/team/widget/manifests/latest", "/v2/team/widget/manifests/" + digest, "/v2/-/blobs/" + digest, "v2/team/manifests/widget/blobs/" + digest, "v2/team/blobs/widget/manifests/release_1.2"} {
		t.Run(path, func(t *testing.T) {
			canonical, wire, reason := OCISourcePath(path)
			if reason != "" || canonical != strings.TrimPrefix(path, "/") || wire != "/"+canonical {
				t.Fatalf("path=%q canonical=%q wire=%q reason=%q", path, canonical, wire, reason)
			}
		})
	}
	for _, path := range []string{"//v2/a/manifests/latest", "v1/a/manifests/latest", "v2/../a/manifests/latest", "v2/a/manifests/%2f", "v2/a/manifests/a?x", "v2/a/manifests/a#x", "v2/a/manifests/a/b", "v2/a/blobs/latest", "v2/-/manifests/latest", "v2/UPPER/manifests/latest", "v2/a/blobs/sha256:" + strings.Repeat("A", 64)} {
		if canonical, wire, reason := OCISourcePath(path); canonical != "" || wire != "" || reason != "oci_source_path_unsupported" {
			t.Fatalf("unsafe/unsupported path accepted: %q => %q %q %q", path, canonical, wire, reason)
		}
	}
}

func TestOCIIdentityDoesNotGuessImageOrTag(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, identity := range []string{"team/widget:release", "team/widget@" + digest} {
		name, reference, ok := ParseOCIIdentity(identity)
		if !ok || name != "team/widget" || reference == "" {
			t.Fatalf("identity %q => %q %q %v", identity, name, reference, ok)
		}
	}
	for _, identity := range []string{"team/widget", "team/widget:sha256:abc", "team/widget@tag", "team/widget:tag@" + digest, "https://example/a:tag", "../a:tag"} {
		if _, _, ok := ParseOCIIdentity(identity); ok {
			t.Fatalf("invalid identity admitted %q", identity)
		}
	}
}
