package backupops_test

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/artifact-gateway/artifact-gateway/internal/backupops"
)

// Only original public release bytes; the downloader verifies independently
// pinned archive/API/SHA256SUMS digests before placing them in this directory.
func TestPublishedNativeRelease(t *testing.T) {
	root := os.Getenv("BACKUPOPS_NATIVE_ASSETS")
	if root == "" {
		t.Skip("run scripts/published-native-backup-test.sh for original published assets")
	}
	for _, r := range []struct {
		version, revision, archiveSHA, binarySHA string
		migrations                               int
	}{
		{"0.5.0", "ea60aea333b29bb60d4ac1b8e2b2a8720563726f", "5ea14b680f71a6db8697000aedaeeeee0b06dc7ef74db17d14ad0e613db0cef1", "d2118e6733513665bc95f5786af91e48a6dbafa7e483b31621da8c9e4289f1bc", 136},
		{"0.6.0", "418e78594a5759a7d15239636ef030b7ab793159", "fb28b2299d7d41845d85d3a0092bef3d21932dec61a3f2544c0b055fcc7f828b", "304e97323b52d6f4cd285777bb62ba1e271094adcb460f975b4046e28da3c270", 140},
	} {
		t.Run(r.version, func(t *testing.T) {
			dir := filepath.Join(root, "native-"+r.version)
			bi, err := buildinfo.ReadFile(filepath.Join(dir, "gateway"))
			if err != nil {
				t.Fatal(err)
			}
			settings := map[string]string{}
			for _, s := range bi.Settings {
				settings[s.Key] = s.Value
			}
			if bi.GoVersion != "go1.26.6" || settings["-trimpath"] != "true" || settings["-ldflags"] != "" {
				t.Fatal("published build shape changed")
			}
			if digestFile(t, filepath.Join(dir, "gateway")) != "sha256:"+r.binarySHA {
				t.Fatal("original binary bytes changed")
			}
			archive := filepath.Join(root, "public-assets", "artifact-gateway_"+r.version+"_linux_amd64.tar.gz")
			if digestFile(t, archive) != "sha256:"+r.archiveSHA {
				t.Fatal("original archive bytes changed")
			}
			identity := backupmanifest.GatewayIdentity{Version: r.version, Revision: r.revision, Artifact: &backupmanifest.SoftwareArtifact{Kind: "binary", SHA256: "sha256:" + r.binarySHA, Platform: "linux/amd64"}}
			release := backupops.Release{Directory: dir, Identity: identity}
			// Decode the additive field so this regression compiles and fails on
			// the original verifier before the field/implementation are added.
			data, _ := json.Marshal(map[string]any{"nativeArchive": map[string]string{"path": archive, "sha256": "sha256:" + r.archiveSHA}})
			if err = json.Unmarshal(data, &release); err != nil {
				t.Fatal(err)
			}
			ledger, count := directoryLedger(t, dir)
			if count != r.migrations {
				t.Fatal("complete published ledger changed")
			}
			m := backupmanifest.Manifest{SchemaVersion: 2, Gateway: identity}
			if err = backupops.VerifyRelease(m, release, bytes.NewReader(ledger)); err != nil {
				t.Fatalf("trusted original published %s release rejected: %v", r.version, err)
			}
			for _, scenario := range []string{"no archive", "untrusted digest", "short digest", "wrong version", "wrong revision", "wrong platform", "wrong binary digest", "replay", "missing ledger", "extra ledger", "wrong ledger checksum", "duplicate ledger", "unsorted ledger", "tampered archive", "tampered binary", "tampered migrations", "isolated VERSION"} {
				t.Run(scenario, func(t *testing.T) {
					candidate := release
					identity := release.Identity
					artifact := *identity.Artifact
					identity.Artifact = &artifact
					archive := *release.NativeArchive
					candidate.NativeArchive = &archive
					altered := append([]byte(nil), ledger...)
					switch scenario {
					case "no archive":
						candidate.NativeArchive = nil
					case "untrusted digest":
						archive.SHA256 = "sha256:" + strings.Repeat("0", 64)
					case "short digest":
						archive.SHA256 = "sha256:" + r.archiveSHA[:12]
					case "wrong version":
						identity.Version = "9.9.9"
					case "wrong revision":
						identity.Revision = strings.Repeat("b", 40)
					case "wrong platform":
						artifact.Platform = "linux/arm64"
					case "wrong binary digest":
						artifact.SHA256 = "sha256:" + strings.Repeat("0", 64)
					case "replay":
						other := "0.5.0"
						if r.version == other {
							other = "0.6.0"
						}
						archive.Path = filepath.Join(root, "public-assets", "artifact-gateway_"+other+"_linux_amd64.tar.gz")
						archive.SHA256 = digestFile(t, archive.Path)
					case "missing ledger":
						altered = []byte("filename\tsha256\n")
					case "extra ledger":
						altered = append(altered, []byte("999999_extra.sql\t"+strings.Repeat("0", 64)+"\n")...)
					case "wrong ledger checksum":
						altered = bytes.Replace(altered, []byte(strings.Split(string(ledger), "\n")[1]), []byte("000001_initial.sql\t"+strings.Repeat("0", 64)), 1)
					case "duplicate ledger":
						altered = append(altered, []byte(strings.Split(string(ledger), "\n")[1]+"\n")...)
					case "unsorted ledger":
						lines := strings.Split(string(altered), "\n")
						lines[1], lines[2] = lines[2], lines[1]
						altered = []byte(strings.Join(lines, "\n"))
					case "tampered archive":
						body, err := os.ReadFile(archive.Path)
						if err != nil {
							t.Fatal(err)
						}
						body[len(body)/2] ^= 1
						archive.Path = filepath.Join(t.TempDir(), "tampered.tar.gz")
						if err = os.WriteFile(archive.Path, body, 0600); err != nil {
							t.Fatal(err)
						}
					case "tampered binary", "tampered migrations", "isolated VERSION":
						candidate.Directory = cloneNativeDirectory(t, release.Directory)
						switch scenario {
						case "tampered binary":
							p := filepath.Join(candidate.Directory, "gateway")
							body, err := os.ReadFile(p)
							if err != nil {
								t.Fatal(err)
							}
							body[len(body)/2] ^= 1
							if err = os.WriteFile(p, body, 0600); err != nil {
								t.Fatal(err)
							}
							// Even a rehashed bundle/spec executable must match the
							// separately approved archive's original gateway bytes.
							artifact.SHA256 = digestFile(t, p)
						case "tampered migrations":
							if err := os.WriteFile(filepath.Join(candidate.Directory, "migrations", "000001_initial.sql"), []byte("SELECT 'forged';\n"), 0600); err != nil {
								t.Fatal(err)
							}
							altered, _ = directoryLedger(t, candidate.Directory)
						default:
							candidate.NativeArchive = nil
							identity.Version = "9.9.9"
							if err := os.WriteFile(filepath.Join(candidate.Directory, "VERSION.txt"), []byte("version=9.9.9\nrevision="+identity.Revision+"\ntarget=linux/amd64\n"), 0600); err != nil {
								t.Fatal(err)
							}
						}
					}
					candidate.Identity = identity
					wrong := m
					wrong.Gateway = identity
					if backupops.VerifyRelease(wrong, candidate, bytes.NewReader(altered)) == nil {
						t.Fatal("untrusted/conflicting original release input accepted")
					}
				})
			}
			t.Logf("original %s binary + digest-pinned archive + full %d ledger verified without executing gateway", r.version, count)
		})
	}
}

func cloneNativeDirectory(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "migrations"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gateway", "VERSION.txt"} {
		body, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(source, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(source, "migrations", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "migrations", e.Name()), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func digestFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func directoryLedger(t *testing.T, dir string) ([]byte, int) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			rows[e.Name()] = strings.TrimPrefix(digestFile(t, filepath.Join(dir, "migrations", e.Name())), "sha256:")
		}
	}
	ledger, err := backupops.MigrationLedger(rows)
	if err != nil {
		t.Fatal(err)
	}
	return ledger, len(rows)
}

// The actual distribution builder invokes this for every supported Linux
// backup artifact, after packaging, with the same release parameters. It never
// runs the gateway; host architecture cannot conceal a static identity failure.
func TestBuiltNativeArchiveIdentity(t *testing.T) {
	dir := os.Getenv("BACKUPOPS_BUILT_RELEASE_DIR")
	if dir == "" {
		t.Skip("actual release builder supplies the full native archive")
	}
	bi, err := buildinfo.ReadFile(filepath.Join(dir, "gateway"))
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, s := range bi.Settings {
		settings[s.Key] = s.Value
	}
	if settings["-trimpath"] != "true" {
		t.Fatal("release acceptance must use actual trimpath build")
	}
	identity := backupmanifest.GatewayIdentity{Version: os.Getenv("BACKUPOPS_BUILT_VERSION"), Revision: os.Getenv("BACKUPOPS_BUILT_REVISION"), Artifact: &backupmanifest.SoftwareArtifact{Kind: "binary", SHA256: digestFile(t, filepath.Join(dir, "gateway")), Platform: os.Getenv("BACKUPOPS_BUILT_PLATFORM")}}
	archive := os.Getenv("BACKUPOPS_BUILT_ARCHIVE")
	release := backupops.Release{Directory: dir, Identity: identity, NativeArchive: &backupops.NativeArchive{Path: archive, SHA256: digestFile(t, archive)}}
	ledger, _ := directoryLedger(t, dir)
	if err := backupops.VerifyRelease(backupmanifest.Manifest{SchemaVersion: 2, Gateway: identity}, release, bytes.NewReader(ledger)); err != nil {
		t.Fatal("actual packaged native release fails offline backup identity:", err)
	}
}
