package v2contract

import (
	"strings"
	"testing"
)

func TestCanonicalRawPathRejectsControlBidiAndOversizedPaths(t *testing.T) {
	for _, path := range []string{
		"release%0Aname.txt",
		"release%E2%80%AEgnp.txt",
		strings.Repeat("a", MaxRawPathBytes+1),
	} {
		if _, ok := CanonicalRawPath(path); ok {
			t.Fatalf("unsafe Raw path was accepted: %q", path)
		}
	}
	if _, ok := CanonicalRawPath("ChatGPT%20Image%20%282%29.png"); !ok {
		t.Fatal("ordinary encoded Unicode-friendly Raw path was rejected")
	}
}

func TestConanReadEndpointIncludesRuntimeMetadataShapes(t *testing.T) {
	const prefix = "/conans/pkg/1.0/user/stable/"
	for _, path := range []string{
		prefix + "revisions/rrev/search",
		prefix + "revisions/rrev/packages/package-id/latest",
	} {
		t.Run(path, func(t *testing.T) {
			if !ConanReadEndpoint("GET", path) {
				t.Fatalf("ConanReadEndpoint rejected %q", path)
			}
		})
	}
}
