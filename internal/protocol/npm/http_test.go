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

func npmTestTarball(t *testing.T, packageJSON string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	body := []byte(packageJSON)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}
