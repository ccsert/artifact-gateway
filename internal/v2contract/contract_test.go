package v2contract

import "testing"

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
