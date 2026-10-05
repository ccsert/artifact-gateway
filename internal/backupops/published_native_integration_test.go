package backupops

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
)

// The same owned PG/RustFS/source/sentinel fixture as the existing recovery
// gate, using official original executables after static trust verification.
// The candidate backup implementation runs RunCLI; old executables remain
// unchanged and supply the source/restored Gateway runtime only.
func TestPublishedNativeRecoveryIntegration(t *testing.T) {
	if os.Getenv("BACKUPOPS_DOCKER_TEST") != "1" || os.Getenv("BACKUPOPS_NATIVE_ASSETS") == "" {
		t.Skip("run make published-native-backup-test for original-asset PG/RustFS recovery")
	}
	for _, version := range []string{"0.5.0", "0.6.0"} {
		t.Run(version, func(t *testing.T) { runLocalRecoveryIntegration(t, "published-"+version) })
	}
}

func publishedNativeFixture(t *testing.T, version string) Release {
	t.Helper()
	data, err := os.ReadFile("testdata/published-native-provenance.json")
	if err != nil {
		t.Fatal(err)
	}
	var pins struct {
		Releases []struct {
			Version, Revision string
			Archive           struct{ Name, SHA256 string }
		}
	}
	if err = json.Unmarshal(data, &pins); err != nil {
		t.Fatal(err)
	}
	for _, pin := range pins.Releases {
		if pin.Version != version {
			continue
		}
		dir := filepath.Join(os.Getenv("BACKUPOPS_NATIVE_ASSETS"), "native-"+version)
		sum, _, err := describeBinary(filepath.Join(dir, "gateway"))
		if err != nil {
			t.Fatal(err)
		}
		release := Release{Directory: dir, Identity: backupmanifest.GatewayIdentity{Version: pin.Version, Revision: pin.Revision, Artifact: &backupmanifest.SoftwareArtifact{Kind: "binary", SHA256: sum, Platform: "linux/amd64"}}, NativeArchive: &NativeArchive{Path: filepath.Join(os.Getenv("BACKUPOPS_NATIVE_ASSETS"), "public-assets", pin.Archive.Name), SHA256: pin.Archive.SHA256}}
		entries, err := os.ReadDir(filepath.Join(dir, "migrations"))
		if err != nil {
			t.Fatal(err)
		}
		rows := map[string]string{}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			f, err := os.Open(filepath.Join(dir, "migrations", e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			digest, _, err := hashReader(f)
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
			rows[e.Name()] = strings.TrimPrefix(digest, "sha256:")
		}
		ledger, err := MigrationLedger(rows)
		if err != nil {
			t.Fatal(err)
		}
		if err = VerifyRelease(backupmanifest.Manifest{SchemaVersion: 2, Gateway: release.Identity}, release, bytes.NewReader(ledger)); err != nil {
			t.Fatal("published runtime not independently trusted:", err)
		}
		return release
	}
	t.Fatal("published release pin missing")
	return Release{}
}
