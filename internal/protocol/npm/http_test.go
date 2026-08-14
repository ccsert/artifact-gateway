package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

func TestParsePathSupportsScopedPackagesAndTarballs(t *testing.T) {
	metadata, ok := ParsePath("/npm/releases/@scope%2Fwidget")
	if !ok || metadata.Repository != "releases" || metadata.Package != "@scope/widget" || metadata.Kind != RoutePackage {
		t.Fatalf("metadata=%#v ok=%t", metadata, ok)
	}
	tarball, ok := ParsePath("/npm/releases/@scope/widget/-/widget-1.2.3.tgz")
	if !ok || tarball.Package != "@scope/widget" || tarball.Tarball != "widget-1.2.3.tgz" || tarball.Kind != RouteTarball {
		t.Fatalf("tarball=%#v ok=%t", tarball, ok)
	}
	if _, ok := ParsePath("/npm/releases/@scope%2F..%2Fsecret"); ok {
		t.Fatal("path traversal package was accepted")
	}
}

func TestParsePathSupportsPackageVersionMetadata(t *testing.T) {
	version, ok := ParsePath("/npm/releases/pnpm/10.7.1")
	if !ok || version.Repository != "releases" || version.Package != "pnpm" || version.Version != "10.7.1" || version.Kind != RouteVersion {
		t.Fatalf("version=%#v ok=%t", version, ok)
	}
	scoped, ok := ParsePath("/npm/releases/@scope%2Fwidget/1.2.3-beta.1")
	if !ok || scoped.Package != "@scope/widget" || scoped.Version != "1.2.3-beta.1" || scoped.Kind != RouteVersion {
		t.Fatalf("scoped=%#v ok=%t", scoped, ok)
	}
	for _, route := range []string{
		"/npm/releases/pnpm/latest",
		"/npm/releases/pnpm/../../secret",
		"/npm/releases/@scope%2Fwidget/not-a-version",
	} {
		if parsed, accepted := ParsePath(route); accepted {
			t.Fatalf("invalid version route %q accepted as %#v", route, parsed)
		}
	}
}

func TestValidVersionUsesStrictSemVer(t *testing.T) {
	for _, version := range []string{"0.0.0", "1.2.3", "1.2.3-beta.1", "1.2.3+build.7", "1.2.3-rc.1+linux"} {
		if !ValidVersion(version) {
			t.Errorf("valid version %q was rejected", version)
		}
	}
	for _, version := range []string{"v1.2.3", "1.2", "01.2.3", "1.2.3-01", "1.2.3+", "latest"} {
		if ValidVersion(version) {
			t.Errorf("invalid version %q was accepted", version)
		}
	}
}

func TestValidPublishAttachmentNameSupportsNPMScopedKey(t *testing.T) {
	if !ValidPublishAttachmentName("@artifact-gateway/npm-demo-2.0.0.tgz", "@artifact-gateway/npm-demo", "2.0.0") {
		t.Fatal("npm scoped attachment key was rejected")
	}
	for _, name := range []string{"@other/npm-demo-2.0.0.tgz", "@artifact-gateway/../npm-demo-2.0.0.tgz"} {
		if ValidPublishAttachmentName(name, "@artifact-gateway/npm-demo", "2.0.0") {
			t.Fatalf("invalid scoped attachment %q was accepted", name)
		}
	}
}

func TestValidateTarballChecksPackageIdentity(t *testing.T) {
	body := npmTestTarball(t, `{"name":"@scope/widget","version":"1.2.3"}`)
	if err := ValidateTarball(body, "@scope/widget", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTarball(body, "@scope/widget", "2.0.0"); err == nil {
		t.Fatal("mismatched package identity was accepted")
	}
}

func TestValidateTarballAcceptsOneCanonicalLegacyRoot(t *testing.T) {
	body := npmTestTarballEntries(t, []npmTestTarEntry{
		{Name: "json-schema/", Directory: true},
		{Name: "json-schema/package.json", Body: `{"name":"@types/json-schema","version":"7.0.15"}`},
		{Name: "json-schema/index.d.ts", Body: "export interface JSONSchema {}\n"},
	})
	if err := ValidateTarball(body, "@types/json-schema", "7.0.15"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateTarballRejectsUnsafeOrAmbiguousManifestRoots(t *testing.T) {
	identity := `{"name":"@types/json-schema","version":"7.0.15"}`
	tests := []struct {
		name    string
		entries []npmTestTarEntry
	}{
		{
			name: "absolute path",
			entries: []npmTestTarEntry{
				{Name: "json-schema/package.json", Body: identity},
				{Name: "/json-schema/index.d.ts", Body: "export {}\n"},
			},
		},
		{
			name: "parent traversal",
			entries: []npmTestTarEntry{
				{Name: "json-schema/package.json", Body: identity},
				{Name: "json-schema/../package/index.js", Body: "module.exports = {}\n"},
			},
		},
		{
			name: "nested manifest",
			entries: []npmTestTarEntry{
				{Name: "json-schema/nested/package.json", Body: identity},
			},
		},
		{
			name: "multiple manifest roots",
			entries: []npmTestTarEntry{
				{Name: "package/package.json", Body: identity},
				{Name: "json-schema/package.json", Body: identity},
			},
		},
		{
			name: "duplicate manifest",
			entries: []npmTestTarEntry{
				{Name: "json-schema/package.json", Body: identity},
				{Name: "json-schema/package.json", Body: identity},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateTarball(npmTestTarballEntries(t, tt.entries), "@types/json-schema", "7.0.15"); err == nil {
				t.Fatal("unsafe or ambiguous tarball was accepted")
			}
		})
	}
}

type npmTestTarEntry struct {
	Name      string
	Body      string
	Directory bool
}

func npmTestTarball(t *testing.T, packageJSON string) []byte {
	t.Helper()
	return npmTestTarballEntries(t, []npmTestTarEntry{{Name: "package/package.json", Body: packageJSON}})
}

func npmTestTarballEntries(t *testing.T, entries []npmTestTarEntry) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		body := []byte(entry.Body)
		header := &tar.Header{Name: entry.Name, Mode: 0o644, Size: int64(len(body))}
		if entry.Directory {
			header.Typeflag = tar.TypeDir
			header.Mode = 0o755
			header.Size = 0
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if !entry.Directory {
			if _, err := tarWriter.Write(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
