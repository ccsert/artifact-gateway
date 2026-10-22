package helm_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	helm "github.com/ccsert/artifact-gateway/internal/protocol/helm"
	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/clearsign"
	"golang.org/x/crypto/openpgp/packet"
)

type entry struct {
	name, body string
	kind       byte
	size       int64
}

func archive(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(e.body))
		if e.size != 0 {
			size = e.size
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Size: size, Typeflag: kind, Mode: 0600, Linkname: "outside"}); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A deliberately oversized header may be truncated; inspectors must reject
	// it without needing its claimed bytes to exist.
	_ = tw.Close()
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestHelmArchiveUsesOriginalChartYAMLAndFullArchive(t *testing.T) {
	chart := []entry{{name: "owned/Chart.yaml", body: "apiVersion: v2\nname: owned\nversion: 1.2.3+build.4\nappVersion: \"42\"\nannotations:\n  team: fixture\n"}, {name: "owned/templates/config.yaml", body: "kind: ConfigMap\n"}}
	data := archive(t, chart...)
	in, err := helm.Inspect(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if in.Metadata.Name != "owned" || in.Metadata.Version != "1.2.3+build.4" || string(in.ChartYAML) != chart[0].body || !bytes.Contains(in.MetadataJSON, []byte(`"appVersion":"42"`)) {
		t.Fatalf("identity or original bytes lost: %+v", in)
	}
}
func TestHelmArchiveRejectsUnsafeIncompleteAndAmbiguousInputs(t *testing.T) {
	valid := entry{name: "owned/Chart.yaml", body: "apiVersion: v2\nname: owned\nversion: 1.0.0\n"}
	cases := map[string][]entry{
		"missing-chart":       {{name: "owned/values.yaml", body: "a: b"}},
		"duplicate-chart":     {valid, valid},
		"traversal":           {valid, {name: "owned/../escape", body: "bad"}},
		"absolute":            {valid, {name: "/etc/owned", body: "bad"}},
		"backslash":           {valid, {name: "owned\\evil", body: "bad"}},
		"symlink":             {valid, {name: "owned/link", kind: tar.TypeSymlink}},
		"hardlink":            {valid, {name: "owned/link", kind: tar.TypeLink}},
		"second-root":         {valid, {name: "foreign/file", body: "bad"}},
		"chart-name-mismatch": {{name: "owned/Chart.yaml", body: "apiVersion: v2\nname: foreign\nversion: 1.0.0\n"}},
		"version-not-semver":  {{name: "owned/Chart.yaml", body: "apiVersion: v2\nname: owned\nversion: latest\n"}},
		"oversized-expanded":  {valid, {name: "owned/huge", size: helm.MaxExpandedBytes + 1}},
		"oversized-metadata":  {{name: "owned/Chart.yaml", size: 1<<20 + 1}},
		"duplicate-yaml-key":  {{name: "owned/Chart.yaml", body: valid.body + "name: foreign\n"}},
		"yaml-alias":          {{name: "owned/Chart.yaml", body: valid.body + "annotations: &a {team: test}\nsources: [*a]\n"}},
		"extra-document":      {{name: "owned/Chart.yaml", body: valid.body + "---\nname: foreign\n"}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := helm.Inspect(bytes.NewReader(archive(t, entries...))); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
	good := archive(t, valid)
	corrupt := append([]byte(nil), good...)
	corrupt[len(corrupt)-8] ^= 1
	for name, data := range map[string][]byte{"truncated-gzip": good[:len(good)-4], "bad-checksum": corrupt, "trailing-garbage": append(append([]byte(nil), good...), 1), "second-member": append(append([]byte(nil), good...), good...)} {
		t.Run(name, func(t *testing.T) {
			if _, err := helm.Inspect(bytes.NewReader(data)); err == nil {
				t.Fatal("invalid gzip accepted")
			}
		})
	}
}
func TestHelmProvenanceBindsChartMetadataFilenameAndDigest(t *testing.T) {
	data := archive(t, entry{name: "owned/Chart.yaml", body: "apiVersion: v2\nname: owned\nversion: 1.0.0\n"})
	in, err := helm.Inspect(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	key, err := openpgp.NewEntity("Owned Helm Test", "", "owned@example.invalid", &packet.Config{RSABits: 2048})
	if err != nil {
		t.Fatal(err)
	}
	sign := func(text string) []byte {
		var out bytes.Buffer
		w, e := clearsign.Encode(&out, key.PrivateKey, nil)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = io.WriteString(w, text); e != nil {
			t.Fatal(e)
		}
		if e = w.Close(); e != nil {
			t.Fatal(e)
		}
		return out.Bytes()
	}
	message := string(in.ChartYAML) + "\n...\nfiles:\n  owned-1.0.0.tgz: " + digest + "\n"
	original := sign(message)
	if err = helm.InspectProvenance(original, in, digest); err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string][]byte{"wrong-chart": sign(strings.Replace(message, "name: owned", "name: foreign", 1)), "wrong-filename": sign(strings.Replace(message, "owned-1.0.0.tgz", "foreign-1.0.0.tgz", 1)), "wrong-digest": sign(strings.Replace(message, digest, "sha256:"+strings.Repeat("0", 64), 1)), "extra-trailer": append(append([]byte(nil), original...), []byte("secret")...), "unsigned": []byte(message)} {
		t.Run(name, func(t *testing.T) {
			if helm.InspectProvenance(payload, in, digest) == nil {
				t.Fatal("invalid provenance accepted")
			}
		})
	}
}
