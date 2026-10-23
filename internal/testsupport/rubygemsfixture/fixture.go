// Package rubygemsfixture builds owned protocol data without running Ruby.
// Real client acceptance separately uses the fixed official Ruby toolchain.
package rubygemsfixture

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"io"
	"testing"
)

func YAML(name, version, platform, summary, deps string) []byte {
	return []byte(fmt.Sprintf(`--- !ruby/object:Gem::Specification
name: %s
version: !ruby/object:Gem::Version
  version: '%s'
platform: %s
authors: [Gateway]
summary: %s
description: Owned fixture
licenses: [MIT]
specification_version: 4
files: [lib/owned.rb]
dependencies:%s
required_ruby_version: !ruby/object:Gem::Requirement
  requirements:
  - - ">="
    - !ruby/object:Gem::Version
      version: '0'
required_rubygems_version: !ruby/object:Gem::Requirement
  requirements:
  - - ">="
    - !ruby/object:Gem::Version
      version: '0'
metadata:
  source_code_uri: https://example.invalid/owned
extra_bounded_tail: [one, two]
`, name, version, platform, summary, deps))
}
func Dependency(name, kind string) string {
	return fmt.Sprintf(`
- !ruby/object:Gem::Dependency
  name: %s
  type: :%s
  requirement: !ruby/object:Gem::Requirement
    requirements:
    - - ">="
      - !ruby/object:Gem::Version
        version: '1.0'
    - - "<"
      - !ruby/object:Gem::Version
        version: '2.0'
  prerelease: false
  extra_dependency_tail: benign
`, name, kind)
}
func gzipBytes(t testing.TB, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	z := gzip.NewWriter(&out)
	if _, e := z.Write(b); e != nil {
		t.Fatal(e)
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}
func Tar(t testing.TB, names []string, bodies [][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for i, name := range names {
		body := bodies[i]
		if e := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(body))}); e != nil {
			t.Fatal(e)
		}
		if _, e := tw.Write(body); e != nil {
			t.Fatal(e)
		}
	}
	if e := tw.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}
func Build(t testing.TB, metadata []byte, payloadName string) []byte {
	t.Helper()
	return BuildPayload(t, metadata, Tar(t, []string{payloadName}, [][]byte{[]byte("OWNED = true\n")}))
}
func BuildPayload(t testing.TB, metadata, payload []byte) []byte {
	t.Helper()
	meta := gzipBytes(t, metadata)
	data := gzipBytes(t, payload)
	shaMeta := sha256.Sum256(meta)
	shaData := sha256.Sum256(data)
	sha512Meta := sha512.Sum512(meta)
	sha512Data := sha512.Sum512(data)
	checks := []byte(fmt.Sprintf("---\nSHA256:\n  metadata.gz: %x\n  data.tar.gz: %x\nSHA512:\n  metadata.gz: %x\n  data.tar.gz: %x\n", shaMeta, shaData, sha512Meta, sha512Data))
	return Tar(t, []string{"metadata.gz", "data.tar.gz", "checksums.yaml.gz"}, [][]byte{meta, data, gzipBytes(t, checks)})
}
func Parts(t testing.TB, data []byte) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(tr)
		if e != nil {
			t.Fatal(e)
		}
		out[h.Name] = b
	}
	return out
}
