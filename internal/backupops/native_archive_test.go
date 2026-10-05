package backupops_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"debug/buildinfo"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/artifact-gateway/artifact-gateway/internal/backupops"
)

type nativeEntry struct {
	name, body, link string
	kind             byte
	size             int64
}

func nativeEntries(t *testing.T, release backupops.Release) []nativeEntry {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(release.Directory, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	g := release.Identity
	return []nativeEntry{
		{name: "./", kind: tar.TypeDir},
		{name: "./VERSION.txt", body: "version=" + g.Version + "\nrevision=" + g.Revision + "\ntarget=" + g.Artifact.Platform + "\n"},
		{name: "./gateway", body: read("gateway")},
		{name: "./migrations/", kind: tar.TypeDir},
		{name: "./migrations/000001_synthetic.sql", body: read("migrations/000001_synthetic.sql")},
	}
}

func writeNativeArchive(t *testing.T, entries []nativeEntry) *backupops.NativeArchive {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	w := tar.NewWriter(z)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(entry.body))
		if entry.size != 0 {
			size = entry.size
		}
		if err := w.WriteHeader(&tar.Header{Name: entry.name, Typeflag: kind, Linkname: entry.link, Mode: 0600, Size: size}); err != nil {
			t.Fatal(err)
		}
		if entry.body != "" {
			if _, err := w.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Explicit oversized headers intentionally produce a truncated fixture.
	_ = w.Close()
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "synthetic.tar.gz")
	if err := os.WriteFile(name, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return &backupops.NativeArchive{Path: name, SHA256: digestFile(t, name)}
}

func TestNativeArchiveProofRejectsAmbiguousOrUnsafeInputs(t *testing.T) {
	m, release, ledger := binaryRelease(t)
	base := nativeEntries(t, release)
	release.NativeArchive = writeNativeArchive(t, base)
	if err := backupops.VerifyRelease(m, release, bytes.NewReader(ledger)); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"traversal", "absolute", "backslash", "alias", "root wrapper", "duplicate", "duplicate alias", "symlink", "hardlink", "unexpected file", "missing gateway", "missing VERSION", "missing migration", "extra migration", "changed migration", "wrong VERSION", "oversized VERSION", "oversized entry", "many entries", "CRC", "trailing gzip member", "symlink archive", "missing archive", "relative archive", "empty digest"} {
		t.Run(scenario, func(t *testing.T) {
			entries := append([]nativeEntry(nil), base...)
			candidate := release
			switch scenario {
			case "traversal":
				entries[1].name = "../VERSION.txt"
			case "absolute":
				entries[1].name = "/VERSION.txt"
			case "backslash":
				entries[1].name = `.\VERSION.txt`
			case "alias":
				entries[1].name = "./nested/../VERSION.txt"
			case "root wrapper":
				entries[1].name = "package/VERSION.txt"
			case "duplicate":
				entries = append(entries, entries[1])
			case "duplicate alias":
				copy := entries[1]
				copy.name = "VERSION.txt"
				entries = append(entries, copy)
			case "symlink":
				entries[1] = nativeEntry{name: "VERSION.txt", kind: tar.TypeSymlink, link: "/foreign"}
			case "hardlink":
				entries[1] = nativeEntry{name: "VERSION.txt", kind: tar.TypeLink, link: "INSTALL.txt"}
			case "unexpected file":
				entries = append(entries, nativeEntry{name: "identity.json", body: "untrusted"})
			case "missing gateway":
				entries = append(entries[:2], entries[3:]...)
			case "missing VERSION":
				entries = append(entries[:1], entries[2:]...)
			case "missing migration":
				entries = entries[:4]
			case "extra migration":
				entries = append(entries, nativeEntry{name: "migrations/000002_extra.sql", body: "SELECT 2;"})
			case "changed migration":
				entries[4].body = "SELECT 2;"
			case "wrong VERSION":
				entries[1].body = strings.Replace(entries[1].body, "synthetic-v1", "forged-v2", 1)
			case "oversized VERSION":
				entries[1].body = strings.Repeat("a", 4097)
			case "oversized entry":
				entries = append(entries, nativeEntry{name: "gateway-healthcheck", size: 256<<20 + 1})
			case "many entries":
				for i := 0; i < 4096; i++ {
					entries = append(entries, nativeEntry{name: fmt.Sprintf("migrations/%06d_synthetic.sql", 800000+i), body: "SELECT 1;"})
				}
			}
			candidate.NativeArchive = writeNativeArchive(t, entries)
			switch scenario {
			case "CRC", "trailing gzip member":
				b, err := os.ReadFile(candidate.NativeArchive.Path)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "CRC" {
					b[len(b)-8] ^= 1
				} else {
					var suffix bytes.Buffer
					z := gzip.NewWriter(&suffix)
					_, _ = z.Write([]byte(""))
					_ = z.Close()
					b = append(b, suffix.Bytes()...)
				}
				if err = os.WriteFile(candidate.NativeArchive.Path, b, 0600); err != nil {
					t.Fatal(err)
				}
				candidate.NativeArchive.SHA256 = digestFile(t, candidate.NativeArchive.Path)
			case "symlink archive":
				link := filepath.Join(t.TempDir(), "alias.tar.gz")
				if err := os.Symlink(candidate.NativeArchive.Path, link); err != nil {
					t.Fatal(err)
				}
				candidate.NativeArchive.Path = link
			case "missing archive":
				candidate.NativeArchive.Path = filepath.Join(t.TempDir(), "missing.tar.gz")
			case "relative archive":
				candidate.NativeArchive.Path = "synthetic.tar.gz"
			case "empty digest":
				candidate.NativeArchive.SHA256 = ""
			}
			if backupops.VerifyRelease(m, candidate, bytes.NewReader(ledger)) == nil {
				t.Fatal("unsafe or unbound archive accepted")
			}
		})
	}
}

func TestArchiveNeverOverridesConflictingStaticOrModuleIdentity(t *testing.T) {
	m, release, ledger := binaryRelease(t)
	wrong := m
	wrong.Gateway.Version = "forged-v2"
	release.Identity = wrong.Gateway
	release.NativeArchive = writeNativeArchive(t, nativeEntries(t, release))
	if backupops.VerifyRelease(wrong, release, bytes.NewReader(ledger)) == nil {
		t.Fatal("archive overrode injected version")
	}
	m, release, ledger = binaryRelease(t)
	p := filepath.Join(release.Directory, "gateway")
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body = bytes.ReplaceAll(body, []byte("github.com/artifact-gateway/artifact-gateway"), []byte("github.com/artifact-gateway/artifact-gatewaX"))
	if err = os.WriteFile(p, body, 0600); err != nil {
		t.Fatal(err)
	}
	bi, err := buildinfo.ReadFile(p)
	if err != nil || bi.Main.Path == "github.com/artifact-gateway/artifact-gateway" {
		t.Fatal("wrong-module static fixture unavailable")
	}
	m.Gateway.Artifact.SHA256 = digestFile(t, p)
	release.Identity = m.Gateway
	release.NativeArchive = writeNativeArchive(t, nativeEntries(t, release))
	if backupops.VerifyRelease(m, release, bytes.NewReader(ledger)) == nil {
		t.Fatal("trusted digest concealed wrong module")
	}
}

func TestArchiveMismatchFailsBeforeTransferWrites(t *testing.T) {
	_, release, ledger := binaryRelease(t)
	release.NativeArchive = writeNativeArchive(t, nativeEntries(t, release))
	scope := backupmanifest.WriterEvidence{ScopeID: "synthetic-scope", InventoryDeclaredComplete: true, Writers: []backupmanifest.Writer{{ID: "synthetic-writer"}}}
	source := &fakeSource{ledger: ledger, objects: map[string][]byte{"synthetic/a": []byte("marker"), "synthetic/b": {}}}
	bundle := filepath.Join(t.TempDir(), "bundle")
	if _, err := backupops.Export(context.Background(), source, bundle, release, scope, "synthetic-archive"); err != nil {
		t.Fatal(err)
	}
	copy := *release.NativeArchive
	release.NativeArchive = &copy
	copy.SHA256 = "sha256:" + strings.Repeat("0", 64)
	target := &fakeTarget{ledger: ledger, objects: map[string][]byte{}}
	if _, err := backupops.Restore(context.Background(), bundle, release, target); err == nil || target.writes != 0 {
		t.Fatal("archive mismatch wrote restore target")
	}
	newBundle := filepath.Join(t.TempDir(), "rejected")
	if _, err := backupops.Export(context.Background(), source, newBundle, release, scope, "synthetic-rejected"); err == nil {
		t.Fatal("unapproved archive exported")
	}
	if _, err := os.Stat(newBundle); !os.IsNotExist(err) {
		t.Fatal("rejected export created bundle")
	}
}

func TestNativeArchivePrivateSpecFieldRemainsStrict(t *testing.T) {
	for _, scenario := range []struct {
		name, body string
		code       int
	}{
		{"additive field", `{"release":{"nativeArchive":{"path":"/synthetic/original.tar.gz","sha256":"sha256:` + strings.Repeat("a", 64) + `"}}}`, 1},
		{"unknown proof", `{"release":{"nativeArchive":{"path":"/synthetic/original.tar.gz","sha256":"sha256:` + strings.Repeat("a", 64) + `","approved":true}}}`, 2},
		{"duplicate digest", `{"release":{"nativeArchive":{"path":"/synthetic/original.tar.gz","sha256":"a","sha256":"b"}}}`, 2},
		{"duplicate proof", `{"release":{"nativeArchive":{},"nativeArchive":{}}}`, 2},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private-spec.json")
			if err := os.WriteFile(path, []byte(scenario.body), 0600); err != nil {
				t.Fatal(err)
			}
			var out, diagnostics bytes.Buffer
			code := backupops.RunCLI(context.Background(), []string{"export", "--spec", path, "--bundle", filepath.Join(t.TempDir(), "bundle")}, &out, &diagnostics)
			// The decoded additive field reaches the existing source-scope
			// rejection before any connection; malformed proof JSON fails usage.
			if code != scenario.code {
				t.Fatalf("spec decoding exit=%d expected=%d", code, scenario.code)
			}
			if scenario.code == 1 && (!strings.Contains(out.String(), "source_spec_or_release_invalid") || diagnostics.Len() != 0) {
				t.Fatal("additive spec did not preserve safe report boundary")
			}
		})
	}
}

type archiveZeros struct{}

func (archiveZeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestNativeArchiveCompressedAndDecodedLimits(t *testing.T) {
	m, release, ledger := binaryRelease(t)
	proof := writeNativeArchive(t, nativeEntries(t, release))
	f, err := os.Open(proof.Path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "decoded-bomb.tar.gz")
	out, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	encoded := gzip.NewWriter(out)
	if _, err = io.Copy(encoded, z); err != nil {
		t.Fatal(err)
	}
	_ = z.Close()
	_ = f.Close()
	// Zero padding is structurally allowed, but cannot expand without bound.
	// Streaming construction keeps this fixture's memory and disk use small.
	if _, err = io.CopyN(encoded, archiveZeros{}, 1<<30); err != nil {
		t.Fatal(err)
	}
	if err = encoded.Close(); err != nil {
		t.Fatal(err)
	}
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
	release.NativeArchive = &backupops.NativeArchive{Path: name, SHA256: digestFile(t, name)}
	if backupops.VerifyRelease(m, release, bytes.NewReader(ledger)) == nil {
		t.Fatal("decoded archive bomb accepted")
	}
	name = filepath.Join(t.TempDir(), "compressed-oversized.tar.gz")
	out, err = os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if err = out.Truncate(512<<20 + 1); err != nil {
		t.Fatal(err)
	}
	_ = out.Close()
	release.NativeArchive = &backupops.NativeArchive{Path: name, SHA256: "sha256:" + strings.Repeat("0", 64)}
	if backupops.VerifyRelease(m, release, bytes.NewReader(ledger)) == nil {
		t.Fatal("oversized compressed archive accepted")
	}
}
