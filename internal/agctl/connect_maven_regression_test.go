package agctl

import (
	"fmt"
	"strings"
	"testing"
)

func TestRepoConnectMavenUsesTheNativePublicationEndpoint(t *testing.T) {
	for _, kind := range []string{"hosted", "proxy"} {
		t.Run(kind, func(t *testing.T) {
			api := newRouteAPI(t, "/gw")
			api.on("GET", repoBase, 200, fmt.Sprintf(`{"id":%q,"name":"maven-target","format":"maven","type":%q,"state":"active","version":"1","anonymousRead":false,"mavenStrictPublication":false}`, testRepoID, kind))
			code, stdout, stderr := runCLI(t, "repo", "connect", testRepoID, "--json")
			if code != ExitOK {
				t.Fatalf("repo connect exit %d: %s", code, stderr)
			}
			prefix := "/maven/"
			if kind == "hosted" {
				prefix = "/repository/maven/"
			}
			want := api.server.URL + "/gw" + prefix + "maven-target/"
			if !strings.Contains(stdout, want) {
				t.Fatalf("Maven %s configuration must use %s: %s", kind, want, stdout)
			}
			if kind == "proxy" && strings.Contains(stdout, "distributionManagement") {
				t.Fatal("a Proxy repository cannot advertise publication")
			}
		})
	}
}
