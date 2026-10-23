package rubygems_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	p "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	f "github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
)

func TestRubyGemArchivePreservesDependenciesAcrossBenignTrailingFields(t *testing.T) {
	for _, platform := range []string{"ruby", "x86_64-linux"} {
		t.Run(platform, func(t *testing.T) {
			gem := f.Build(t, f.YAML("owned", "1.2.3.pre", platform, "fixture", f.Dependency("leaf", "runtime")), "lib/owned.rb")
			in, e := p.Inspect(bytes.NewReader(gem))
			if e != nil {
				t.Fatal(e)
			}
			if in.Metadata.Name != "owned" || in.Metadata.Version != "1.2.3.pre" || in.Metadata.Platform != platform || len(in.Metadata.Dependencies) != 1 || in.Metadata.Dependencies[0].Name != "leaf" || len(in.Metadata.Dependencies[0].Requirements) != 2 {
				t.Fatalf("metadata lost: %+v", in.Metadata)
			}
			info := string(p.Info([]p.IndexEntry{{Metadata: in.Metadata, Digest: "sha256:" + strings.Repeat("a", 64)}}))
			if !strings.Contains(info, "leaf:>= 1.0&< 2.0") || !strings.Contains(info, "checksum:"+strings.Repeat("a", 64)) {
				t.Fatal(info)
			}
		})
	}
}
func TestRubyGemArchiveRejectsUnsafeIncompleteAmbiguousAndUnboundedInput(t *testing.T) {
	base := f.YAML("owned", "1.0.0", "ruby", "fixture", " []")
	gem := f.Build(t, base, "lib/owned.rb")
	parts := f.Parts(t, gem)
	tests := map[string][]byte{"truncated": gem[:len(gem)/2], "corrupt-gzip": append([]byte(nil), gem...), "missing-checksum": f.Tar(t, []string{"metadata.gz", "data.tar.gz"}, [][]byte{parts["metadata.gz"], parts["data.tar.gz"]}), "duplicate": f.Tar(t, []string{"metadata.gz", "metadata.gz"}, [][]byte{parts["metadata.gz"], parts["metadata.gz"]}), "traversal": f.Build(t, base, "../owned.rb"), "absolute": f.Build(t, base, "/owned.rb"), "duplicate-yaml": f.Build(t, append(append([]byte{}, base...), []byte("name: stolen\n")...), "lib/owned.rb"), "object-code": f.Build(t, bytes.Replace(base, []byte("summary: fixture"), []byte("summary: !ruby/object:ERB {src: evil}"), 1), "lib/owned.rb"), "alias": f.Build(t, append(append([]byte{}, base...), []byte("x: &foo [one]\ny: *foo\n")...), "lib/owned.rb"), "wrong-identity": f.Build(t, bytes.Replace(base, []byte("name: owned"), []byte("name: ../owned"), 1), "lib/owned.rb"), "yaml-bomb": f.Build(t, append(append([]byte{}, base...), []byte("extra: "+strings.Repeat("a", int(p.MaxMetadataBytes))+"\n")...), "lib/owned.rb"), "trailing-archive": append(append([]byte{}, gem...), gem...)}
	tests["corrupt-gzip"][530] ^= 0xff
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, e := p.Inspect(bytes.NewReader(data)); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestRubyGemLegacyAndCompactVersionPlatformIndexesAgree(t *testing.T) {
	entries := []p.IndexEntry{}
	for _, version := range []string{"1.10.0", "1.2.0", "2.0.pre"} {
		m, e := p.ParseMetadata(f.YAML("owned", version, "ruby", "fixture", f.Dependency("leaf", "runtime")))
		if e != nil {
			t.Fatal(e)
		}
		entries = append(entries, p.IndexEntry{Metadata: m, Digest: "sha256:" + strings.Repeat("a", 64)})
	}
	info := string(p.Info(entries))
	if strings.Index(info, "1.2.0 ") > strings.Index(info, "1.10.0 ") {
		t.Fatal(info)
	}
	if !strings.Contains(string(p.Versions(entries)), "owned 1.2.0,1.10.0,2.0.pre ") {
		t.Fatal(string(p.Versions(entries)))
	}
	for _, kind := range []string{"all", "latest", "prerelease"} {
		z, e := gzip.NewReader(bytes.NewReader(p.LegacySpecs(entries, kind)))
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(z)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.HasPrefix(data, []byte{4, 8}) {
			t.Fatal("missing Marshal version")
		}
	}
	if p.CompareVersion("1.2", "1.2.0") != 0 || p.CompareVersion("2.0.pre", "2.0") >= 0 || p.CompareVersion("1.11", "1.9") <= 0 {
		t.Fatal("version precedence")
	}
}
