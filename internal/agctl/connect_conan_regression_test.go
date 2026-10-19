package agctl

import (
	"fmt"
	"strings"
	"testing"
)

func TestRepoConnectConanUsesCanonicalRemoteAndHostedUpload(t *testing.T) {
	for _, kind := range []string{"hosted", "proxy"} {
		t.Run(kind, func(t *testing.T) {
			api := newRouteAPI(t, "/gw")
			api.on("GET", repoBase, 200, fmt.Sprintf(`{"id":%q,"name":"conan-target","format":"conan","type":%q,"state":"active","version":"1"}`, testRepoID, kind))
			code, out, errout := runCLI(t, "repo", "connect", testRepoID, "--json")
			if code != ExitOK || !strings.Contains(out, api.server.URL+"/gw/conan/conan-target") || strings.Contains(out, "/conan/v2/") {
				t.Fatalf("canonical %s exit=%d out=%s err=%s", kind, code, out, errout)
			}
			if (kind == "hosted") != strings.Contains(out, "--confirm") {
				t.Fatalf("upload guidance %s=%s", kind, out)
			}
		})
	}
}
