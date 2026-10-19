package conan

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
)

func publicationManifest(files map[string]string) ([]byte, string) {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var entries strings.Builder
	for _, name := range names {
		fmt.Fprintf(&entries, "%s: %x\n", name, md5.Sum([]byte(files[name])))
	}
	revision := md5.Sum([]byte(entries.String()))
	return []byte("1700000000\n" + entries.String()), hex.EncodeToString(revision[:])
}

func publicationArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var data bytes.Buffer
	gzipWriter := gzip.NewWriter(&data)
	writer := tar.NewWriter(gzipWriter)
	for name, body := range files {
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestValidateConanPublicationRequiresCompleteManifestTree(t *testing.T) {
	manifest, revision := publicationManifest(map[string]string{"conanfile.py": "recipe", "LICENSE": "license", "export_source/include/header.h": "header"})
	assets := map[string][]byte{
		"conan_export.tgz":  publicationArchive(t, map[string]string{"conanfile.py": "recipe", "LICENSE": "license"}),
		"conan_sources.tgz": publicationArchive(t, map[string]string{"include/header.h": "header"}),
		ManifestName:        manifest,
	}
	for _, tc := range []struct {
		name   string
		change func(map[string][]byte) (string, []byte)
		valid  bool
	}{
		{"complete export and source archives", func(m map[string][]byte) (string, []byte) { return revision, manifest }, true},
		{"missing source archive", func(m map[string][]byte) (string, []byte) { delete(m, "conan_sources.tgz"); return revision, manifest }, false},
		{"wrong file bytes", func(m map[string][]byte) (string, []byte) {
			m["conan_sources.tgz"] = publicationArchive(t, map[string]string{"include/header.h": "changed"})
			return revision, manifest
		}, false},
		{"extra file", func(m map[string][]byte) (string, []byte) {
			m["conanfile.py"] = []byte("recipe")
			return revision, manifest
		}, false},
		{"truncated gzip", func(m map[string][]byte) (string, []byte) {
			b := m["conan_export.tgz"]
			m["conan_export.tgz"] = b[:len(b)-1]
			return revision, manifest
		}, false},
		{"archive traversal", func(m map[string][]byte) (string, []byte) {
			m["conan_sources.tgz"] = publicationArchive(t, map[string]string{"../escape": "bad"})
			return revision, manifest
		}, false},
		{"wrong recipe revision", func(m map[string][]byte) (string, []byte) { return strings.Repeat("0", 32), manifest }, false},
		{"duplicate manifest path", func(m map[string][]byte) (string, []byte) {
			return revision, append(append([]byte(nil), manifest...), []byte("LICENSE: 01234567890123456789012345678901\n")...)
		}, false},
		{"oversized manifest", func(m map[string][]byte) (string, []byte) {
			return revision, bytes.Repeat([]byte("x"), ManifestMaxBytes+1)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyAssets := map[string][]byte{}
			for name, body := range assets {
				copyAssets[name] = body
			}
			wantRevision, body := tc.change(copyAssets)
			names := make([]string, 0, len(copyAssets))
			for name := range copyAssets {
				names = append(names, name)
			}
			err := ValidatePublication(context.Background(), "recipe", wantRevision, "", body, names, func(name string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(copyAssets[name])), nil })
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
}

func TestValidateConanPackageBindsInfoIDAndRevision(t *testing.T) {
	manifest, revision := publicationManifest(map[string]string{"conaninfo.txt": "", "include/header.h": "header"})
	assets := map[string][]byte{ManifestName: manifest, "conaninfo.txt": {}, "conan_package.tgz": publicationArchive(t, map[string]string{"include/header.h": "header"})}
	for _, packageID := range []string{"da39a3ee5e6b4b0d3255bfef95601890afd80709", strings.Repeat("a", 40)} {
		err := ValidatePublication(context.Background(), "package", revision, packageID, manifest, []string{ManifestName, "conaninfo.txt", "conan_package.tgz"}, func(name string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(assets[name])), nil })
		if (err == nil) != (packageID == "da39a3ee5e6b4b0d3255bfef95601890afd80709") {
			t.Fatalf("package ID=%s error=%v", packageID, err)
		}
	}
}

func TestParseConanUploadValidatesCanonicalIdentity(t *testing.T) {
	base := "/conan/hosted/v2/conans/pkg/1.0/_/_/revisions/" + strings.Repeat("a", 32)
	for _, route := range []string{base + "/files/conanfile.py", base + "/packages/" + strings.Repeat("b", 40) + "/revisions/" + strings.Repeat("c", 32) + "/files/conanmanifest.txt"} {
		if _, err := ParseUploadPath(route); err != nil {
			t.Fatal(err)
		}
	}
	for _, route := range []string{base + "/files/%2e%2e", base + "/files/metadata", base + "/packages/bad/revisions/" + strings.Repeat("c", 32) + "/files/conaninfo.txt", strings.Replace(base, "revisions/", "revisions/%2f", 1) + "/files/conanfile.py", base + "/files/../conanfile.py"} {
		if _, err := ParseUploadPath(route); err == nil {
			t.Fatalf("accepted %s", route)
		}
	}
}

func TestConanPublicationRejectsCancelledVerification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidatePublication(ctx, "recipe", "", "", nil, nil, nil); err == nil {
		t.Fatal("cancelled verification accepted")
	}
}
func TestConanArchiveBoundsIncludeDirectoriesAndTarFraming(t *testing.T) {
	archive := publicationArchive(t, map[string]string{"file": "x"})
	budget := int64(1)
	if err := archiveManifest(bytes.NewReader(archive), "", map[string]string{}, &budget); err == nil {
		t.Fatal("tar framing bypassed expanded byte budget")
	}
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for i := 0; i <= ManifestMaxFiles; i++ {
		if err := tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("dir-%d/", i), Typeflag: tar.TypeDir, Mode: 0700}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	budget = ExpandedMaxBytes
	if err := archiveManifest(bytes.NewReader(data.Bytes()), "", map[string]string{}, &budget); err == nil {
		t.Fatal("directory entries bypassed entry count budget")
	}
}
