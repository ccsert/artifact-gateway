package pypi

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"net/url"
	"strings"
	"testing"
)

func fixture(t *testing.T, filename, metadata string, extra bool) []byte {
	t.Helper()
	var b bytes.Buffer
	if strings.HasSuffix(filename, ".whl") {
		w := zip.NewWriter(&b)
		f, err := w.Create("widget-1.0.dist-info/METADATA")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(f, metadata); err != nil {
			t.Fatal(err)
		}
		if extra {
			f, err = w.Create("widget-1.0.dist-info/METADATA")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = io.WriteString(f, metadata); err != nil {
				t.Fatal(err)
			}
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&b)
		w := tar.NewWriter(gz)
		if err := w.WriteHeader(&tar.Header{Name: "widget-1.0/PKG-INFO", Mode: 0o600, Size: int64(len(metadata))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, metadata); err != nil {
			t.Fatal(err)
		}
		if extra {
			if err := w.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o600, Size: 0}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}
func TestImportDistributionIdentityAndArchiveSafety(t *testing.T) {
	for _, filename := range []string{"widget-1.0-py3-none-any.whl", "widget-1.0.tar.gz"} {
		t.Run(filename, func(t *testing.T) {
			data := fixture(t, filename, "Metadata-Version: 2.1\nName: Widget\nVersion: 1.0\nRequires-Python: >=3.8\n", false)
			d, err := Inspect(bytes.NewReader(data), int64(len(data)), filename)
			if err != nil || d.Project != "widget" || d.RequiresPython != ">=3.8" {
				t.Fatalf("distribution=%+v %v", d, err)
			}
			for i, bad := range [][]byte{fixture(t, filename, "Name: other\nVersion: 1.0\n", false), fixture(t, filename, "Name: widget\nName: alias\nVersion: 1.0\n", false), fixture(t, filename, "Name: widget\nVersion: 1.0\n", true), data[:len(data)-5]} {
				if _, err = Inspect(bytes.NewReader(bad), int64(len(bad)), filename); err == nil {
					t.Fatalf("invalid archive %d accepted", i)
				}
			}
		})
	}
	for _, name := range []string{"../widget-1.0.tar.gz", "widget-1.0.zip", "widget-1.0.tgz", "widget-1.0-a-b.whl", "widget-1.0-abc-py3-none-any.whl"} {
		if _, err := FilenameIdentity(name); err == nil {
			t.Fatalf("accepted unsupported %s", name)
		}
	}
}
func TestImportSimpleSelectionBoundary(t *testing.T) {
	base, _ := url.Parse("https://example.com/nexus/repository/source/simple/widget/")
	link := `<a href="../../packages/widget-1.0-py3-none-any.whl#sha256=` + strings.Repeat("a", 64) + `" data-requires-python="&gt;=3.8" data-yanked="bad release">wheel</a>`
	s, err := ParseSimple([]byte(link), "widget", base, "/nexus/repository/source/")
	if err != nil || len(s.Files) != 1 || !s.Files[0].Yanked || s.Files[0].YankReason != "bad release" || s.Files[0].RequiresPython != ">=3.8" {
		t.Fatalf("selection=%+v %v", s, err)
	}
	raw, err := s.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeSnapshot(raw); err != nil {
		t.Fatal(err)
	}
	for i, bad := range []string{link + link, `<base href="https://evil.test/">` + link, strings.Replace(link, "../../packages/", "https://evil.test/", 1), strings.Replace(link, "../../packages/", "../../../other/", 1), strings.Replace(link, "#sha256=", "?credential=secret#sha256=", 1), strings.Replace(link, "#sha256=", "#md5=", 1), strings.Replace(link, " data-yanked=", " href=\"/duplicate\" data-yanked=", 1), strings.Replace(link, "widget-1.0-py3-none-any.whl", "widget-1.0.zip", 1)} {
		if _, err = ParseSimple([]byte(bad), "widget", base, "/nexus/repository/source/"); err == nil {
			t.Fatalf("unsafe or ambiguous Simple %d accepted", i)
		}
	}
}

func TestImportSdistUsesRootMetadataAndKeepsNestedPackageData(t *testing.T) {
	root := "widget-1.0/PKG-INFO"
	nested := "widget-1.0/src/widget.egg-info/PKG-INFO"
	primary := "Metadata-Version: 2.2\nName: widget\nVersion: 1.0\nRequires-Python: >=3.8\n"
	for _, tc := range []struct {
		name  string
		paths []string
		valid bool
	}{
		{"nested before root", []string{nested, root}, true},
		{"root before nested", []string{root, nested}, true},
		{"missing root metadata", []string{nested}, false},
		{"duplicate root metadata", []string{root, root, nested}, false},
		{"second top level root", []string{root, "other-1.0/data.txt"}, false},
		{"unsafe nested path", []string{root, "widget-1.0/../PKG-INFO"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tw := tar.NewWriter(gz)
			for _, name := range tc.paths {
				body := "Name: unrelated-build-data\nVersion: 99.0\n"
				if name == root {
					body = primary
				}
				if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(body))}); err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(tw, body); err != nil {
					t.Fatal(err)
				}
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			d, err := Inspect(bytes.NewReader(archive.Bytes()), int64(archive.Len()), "widget-1.0.tar.gz")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v distribution=%+v error=%v", tc.valid, d, err)
			}
			if tc.valid && (d.Project != "widget" || d.Version != "1.0" || d.RequiresPython != ">=3.8") {
				t.Fatalf("nested build data replaced root identity: %+v", d)
			}
		})
	}
}

func TestImportSimpleCanonicalizesFilenameEscapesWithoutChangingHierarchy(t *testing.T) {
	base, _ := url.Parse("https://example.com/nexus/repository/source/simple/widget/")
	for _, filename := range []string{"widget-1.0+cpu-py3-none-any.whl", "widget-1.0%2Bcpu-py3-none-any.whl", "widget-1.0%2bcpu-py3-none-any.whl"} {
		body := []byte(`<a href="../../packages/` + filename + `#sha256=` + strings.Repeat("a", 64) + `">wheel</a>`)
		s, err := ParseSimple(body, "widget", base, "/nexus/repository/source/")
		if err != nil || len(s.Files) != 1 || s.Files[0].SourcePath != "packages/widget-1.0+cpu-py3-none-any.whl" {
			t.Fatalf("equivalent escape %q changed identity: %+v %v", filename, s, err)
		}
	}
	for _, relative := range []string{"../../packages%2fwidget-1.0-py3-none-any.whl", "%2e%2e/%2e%2e/packages/widget-1.0-py3-none-any.whl", "../../packages/widget-1.0%252Bcpu-py3-none-any.whl", "../../packages%5cwidget-1.0-py3-none-any.whl", "../../../other/widget-1.0-py3-none-any.whl"} {
		body := []byte(`<a href="` + relative + `#sha256=` + strings.Repeat("a", 64) + `">wheel</a>`)
		if _, err := ParseSimple(body, "widget", base, "/nexus/repository/source/"); err == nil {
			t.Fatalf("unsafe encoded reference accepted: %s", relative)
		}
	}
}
