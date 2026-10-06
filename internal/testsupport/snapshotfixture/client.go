package snapshotfixture

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/snapshotimport"
)

func replaceSnapshotFile(t testing.TB, dir string, files map[string][]byte, f *snapshotimport.File, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, f.Path), body, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	f.Digest = "sha256:" + hex.EncodeToString(sum[:])
	f.Size = int64(len(body))
	files[f.Path] = body
}

// SnapshotClientBundle uses valid synthetic POM/JAR bytes for a real resolver.
func SnapshotClientBundle(t testing.TB) (string, string, snapshotimport.Manifest, map[string][]byte) {
	t.Helper()
	dir, _, m, files := SnapshotBundle(t)
	for i := range m.Coordinates[0].Builds {
		for j := range m.Coordinates[0].Builds[i].Files {
			f := &m.Coordinates[0].Builds[i].Files[j]
			body := files[f.Path]
			if strings.HasSuffix(f.Path, ".pom") {
				body = []byte(strings.Replace(string(body), "<project>", "<project><modelVersion>4.0.0</modelVersion>", 1))
			}
			if strings.HasSuffix(f.Path, ".jar") {
				var out bytes.Buffer
				z := zip.NewWriter(&out)
				writer, err := z.Create("marker.txt")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = writer.Write(body); err != nil {
					t.Fatal(err)
				}
				if err = z.Close(); err != nil {
					t.Fatal(err)
				}
				body = out.Bytes()
			}
			replaceSnapshotFile(t, dir, files, f, body)
		}
	}
	return dir, WriteSnapshotManifest(t, dir, m), m, files
}

// SnapshotSignedBundle preserves multipart extensions and independent current
// selectors using invented signature/archive bytes.
func SnapshotSignedBundle(t testing.TB) (string, string, snapshotimport.Manifest, map[string][]byte) {
	t.Helper()
	dir, _, m, files := SnapshotBundle(t)
	for i := range m.Coordinates[0].Builds {
		b := &m.Coordinates[0].Builds[i]
		prefix := strings.TrimSuffix(b.Files[0].Path, ".pom")
		for _, suffix := range []string{".jar.asc", "-sources.jar.asc", ".tar.gz"} {
			f := snapshotimport.File{Path: prefix + suffix}
			replaceSnapshotFile(t, dir, files, &f, []byte("synthetic signature/archive "+prefix+suffix))
			b.Files = append(b.Files, f)
		}
	}
	f := m.Coordinates[0].Metadata
	entries := `<snapshotVersion><extension>jar.asc</extension><value>1.0-20260101.000000-7</value><updated>20260101000000</updated></snapshotVersion><snapshotVersion><extension>jar.asc</extension><classifier>sources</classifier><value>1.0-20260102.000000-7</value><updated>20260102000000</updated></snapshotVersion><snapshotVersion><extension>tar.gz</extension><value>1.0-20260101.000000-7</value><updated>20260101000000</updated></snapshotVersion>`
	replaceSnapshotFile(t, dir, files, f, []byte(strings.Replace(string(files[f.Path]), "</snapshotVersions>", entries+"</snapshotVersions>", 1)))
	return dir, WriteSnapshotManifest(t, dir, m), m, files
}
