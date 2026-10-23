package rubygems_test

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"

	p "github.com/ccsert/artifact-gateway/internal/protocol/rubygems"
	f "github.com/ccsert/artifact-gateway/internal/testsupport/rubygemsfixture"
)

func TestRubyGemRejectsYamlAndTarParserDifferentials(t *testing.T) {
	base := f.YAML("owned", "1.0.0", "ruby", "fixture", f.Dependency("leaf", "runtime"))
	cases := map[string][]byte{
		"platform-sequence":                 bytes.Replace(base, []byte("platform: ruby"), []byte("platform: [x86_64, linux]"), 1),
		"platform-map":                      bytes.Replace(base, []byte("platform: ruby"), []byte("platform: {cpu: x86_64, os: linux}"), 1),
		"platform-bool":                     bytes.Replace(base, []byte("platform: ruby"), []byte("platform: true"), 1),
		"dependency-quoted-symbol-string":   bytes.Replace(base, []byte("type: :runtime"), []byte(`type: ":runtime"`), 1),
		"dependency-explicit-string-symbol": bytes.Replace(base, []byte("type: :runtime"), []byte("type: !!str :runtime"), 1),
		"name-yaml11-boolean":               bytes.Replace(base, []byte("name: owned"), []byte("name: yes"), 1),
		"dependency-string-type":            bytes.Replace(base, []byte("type: :runtime"), []byte("type: runtime"), 1),
		"dependency-name-bool":              bytes.Replace(base, []byte("name: leaf"), []byte("name: true"), 1),
		"specification-version-string":      bytes.Replace(base, []byte("specification_version: 4"), []byte("specification_version: '4'"), 1),
		"version-float":                     bytes.Replace(base, []byte("version: '1.0.0'"), []byte("version: 1.2"), 1),
	}
	for name, yaml := range cases {
		t.Run(name, func(t *testing.T) {
			if _, e := p.Inspect(bytes.NewReader(f.Build(t, yaml, "lib/owned.rb"))); e == nil {
				t.Fatal("cross-parser metadata accepted")
			}
		})
	}
	parts := f.Parts(t, f.Build(t, base, "lib/owned.rb"))
	// Go archive/tar honors PAX path while RubyGems TarReader reads the raw
	// following USTAR filename. A private x header makes the interpretations split.
	paxBody := []byte("20 path=metadata.gz\n")
	// Correct the decimal length until the self-inclusive PAX record is fixed.
	for {
		next := []byte(strconv.Itoa(len(paxBody)) + " path=metadata.gz\n")
		if bytes.Equal(next, paxBody) {
			break
		}
		paxBody = next
	}
	pax := f.Tar(t, []string{"PaxHeader"}, [][]byte{paxBody})
	pax[156] = 'x'
	for i := 148; i < 156; i++ {
		pax[i] = ' '
	}
	sum := 0
	for _, b := range pax[:512] {
		sum += int(b)
	}
	copy(pax[148:156], []byte(fmt.Sprintf("%06o\x00 ", sum)))
	closure := f.Tar(t, []string{"payload.bin", "data.tar.gz", "checksums.yaml.gz"}, [][]byte{parts["metadata.gz"], parts["data.tar.gz"], parts["checksums.yaml.gz"]})
	archive := append(append([]byte{}, pax[:1024]...), closure...)
	t.Run("pax-name-overrides-raw-header", func(t *testing.T) {
		if _, e := p.Inspect(bytes.NewReader(archive)); e == nil {
			t.Fatal("hidden PAX filename accepted")
		}
	})
	t.Run("payload-pax-name-overrides-raw-header", func(t *testing.T) {
		body := []byte("24 path=lib/owned.rb\n")
		for {
			next := []byte(strconv.Itoa(len(body)) + " path=lib/owned.rb\n")
			if bytes.Equal(next, body) {
				break
			}
			body = next
		}
		prefix := f.Tar(t, []string{"PaxHeader"}, [][]byte{body})
		prefix[156] = 'x'
		for i := 148; i < 156; i++ {
			prefix[i] = ' '
		}
		sum := 0
		for _, b := range prefix[:512] {
			sum += int(b)
		}
		copy(prefix[148:156], []byte(fmt.Sprintf("%06o\x00 ", sum)))
		payload := append(append([]byte{}, prefix[:1024]...), f.Tar(t, []string{"payload.rb"}, [][]byte{[]byte("OWNED=true\n")})...)
		if _, e := p.Inspect(bytes.NewReader(f.BuildPayload(t, base, payload))); e == nil {
			t.Fatal("hidden inner PAX filename accepted")
		}
	})
}

func TestRubyGemPlatformDefaultsAndLongUSTARPathsKeepDependencies(t *testing.T) {
	base := f.YAML("owned", "1.0.0", "ruby", "fixture", f.Dependency("leaf", "runtime"))
	for _, value := range []string{"", "platform: ''\n", "platform:\n"} {
		yaml := bytes.Replace(base, []byte("platform: ruby\n"), []byte(value), 1)
		in, e := p.Inspect(bytes.NewReader(f.Build(t, yaml, "lib/owned.rb")))
		if e != nil || in.Metadata.Platform != "ruby" || len(in.Metadata.Dependencies) != 1 {
			t.Fatalf("official ruby platform default %q %+v %v", value, in.Metadata, e)
		}
	}
	longName := "lib/" + strings.Repeat("a", 120) + "/" + strings.Repeat("b", 90) + ".rb"
	yaml := bytes.Replace(base, []byte("lib/owned.rb"), []byte(longName), 1)
	in, e := p.Inspect(bytes.NewReader(f.Build(t, yaml, longName)))
	if e != nil || len(in.Metadata.Files) != 1 || in.Metadata.Files[0] != longName || len(in.Metadata.Dependencies) != 1 {
		t.Fatalf("legal USTAR prefix or tail lost %+v %v", in.Metadata, e)
	}
	for _, platform := range []string{"ruby", "x86_64-linux", "aarch64-linux-musl", "arm64-darwin", "arm64-darwin-23", "x64-mingw-ucrt", "x86-mswin32-60", "java"} {
		if !p.ValidPlatform(platform) {
			t.Errorf("canonical platform rejected %q", platform)
		}
	}
	for _, platform := range []string{"current", "i686-linux", "mswin32", "powerpc-darwin", "arm64-darwin23", "unknown-platform-name"} {
		if p.ValidPlatform(platform) {
			t.Errorf("noncanonical platform accepted %q", platform)
		}
	}
}
