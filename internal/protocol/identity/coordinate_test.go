package identity

import (
	"strings"
	"testing"
)

func TestCanonicalCoordinates(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "maven", got: Maven("org.example:widget:1.0"), want: "org.example:widget:1.0"},
		{name: "oci", got: OCI("team/widget"), want: "team/widget"},
		{name: "raw", got: Raw("releases/widget.bin"), want: "releases/widget.bin"},
		{name: "npm scoped", got: NPMVersion("@team/widget", "1.0.0"), want: "@team/widget@1.0.0"},
		{name: "pypi", got: PyPIVersion("widget", "1.0"), want: "widget@1.0"},
		{name: "go", got: GoVersion("example.com/team/widget", "v1.0.0"), want: "example.com/team/widget@v1.0.0"},
		{name: "nuget", got: NuGetVersion("contoso.utility", "1.2.3-rc.1"), want: "contoso.utility@1.2.3-rc.1"},
		{name: "cargo", got: CargoVersion("demo-crate", "1.2.3"), want: "demo-crate@1.2.3"},
		{name: "apt", got: APTVersion("artifact-gateway", "1:2.3.4-5", "arm64"), want: "artifact-gateway@1:2.3.4-5#arm64"},
		{name: "conan recipe", got: ConanRecipe("widget/1.0@team/stable", "rrev"), want: "widget/1.0@team/stable#rrev"},
		{name: "conan package", got: ConanPackage("widget/1.0@team/stable", "rrev", "pkg", "prev"), want: "widget/1.0@team/stable#rrev/pkg#prev"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.got != test.want {
				t.Fatalf("coordinate=%q want=%q", test.got, test.want)
			}
		})
	}
}

func TestSHA256DigestValidation(t *testing.T) {
	if !IsSHA256Digest("sha256:" + strings.Repeat("a", 64)) {
		t.Fatal("expected canonical digest")
	}
	for _, value := range []string{"", strings.Repeat("a", 64), "sha256:" + strings.Repeat("A", 64), "sha256:abc"} {
		if IsSHA256Digest(value) {
			t.Fatalf("accepted digest %q", value)
		}
	}
}

func TestPostgreSQLCoordinateExpressions(t *testing.T) {
	if got := PostgreSQLNPMVersion("package_name", "version"); got != `package_name || '@' || version` {
		t.Fatalf("npm expression=%q", got)
	}
	if got := PostgreSQLConanPackage("reference", "rrev", "package_id", "prev"); got != `reference || '#' || rrev || '/' || package_id || '#' || prev` {
		t.Fatalf("conan expression=%q", got)
	}
}
