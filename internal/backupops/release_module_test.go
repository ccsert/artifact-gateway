package backupops_test

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ccsert/artifact-gateway/internal/backupmanifest"
	"github.com/ccsert/artifact-gateway/internal/backupops"
)

func TestReleaseModuleIdentityRemainsPairedAcrossNamespaceChange(t *testing.T) {
	const current = "github.com/ccsert/artifact-gateway"
	const legacy = "github.com/artifact-gateway/artifact-gateway"
	const unrelated = "github.com/ccsert/artifact-gatewaX"
	for _, tc := range []struct {
		name, module, flagsModule, patchedField, patchedValue string
		archive, accepted                                     bool
	}{
		{name: "current namespace", module: current, flagsModule: current, accepted: true},
		{name: "legacy namespace", module: legacy, flagsModule: legacy, accepted: true},
		{name: "current archive without linker flags", module: current, archive: true, accepted: true},
		{name: "legacy archive without linker flags", module: legacy, archive: true, accepted: true},
		{name: "current missing linker identity", module: current},
		{name: "legacy missing linker identity", module: legacy},
		{name: "unrelated module with archive", module: unrelated, flagsModule: unrelated, archive: true},
		{name: "current command with legacy main module", module: legacy, flagsModule: legacy, patchedField: "path", patchedValue: current + "/cmd/gateway", archive: true},
		{name: "legacy command with current main module", module: legacy, flagsModule: current, patchedField: "mod", patchedValue: current, archive: true},
		{name: "current module with legacy linker namespace", module: current, flagsModule: legacy, archive: true},
		{name: "legacy module with current linker namespace", module: legacy, flagsModule: current, archive: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, release, ledger := moduleIdentityRelease(t, tc.module, tc.flagsModule)
			if tc.patchedField != "" {
				path := filepath.Join(release.Directory, "gateway")
				old := tc.module
				if tc.patchedField == "path" {
					old += "/cmd/gateway"
				}
				patchReleaseModuleField(t, path, tc.patchedField, old, tc.patchedValue)
				m.Gateway.Artifact.SHA256 = digestFile(t, path)
				release.Identity = m.Gateway
			}
			if tc.archive {
				release.NativeArchive = writeNativeArchive(t, nativeEntries(t, release))
			}
			if err := backupops.VerifyRelease(m, release, bytes.NewReader(ledger)); (err == nil) != tc.accepted {
				t.Fatalf("VerifyRelease accepted=%t, want %t: %v", err == nil, tc.accepted, err)
			}
		})
	}
}

// moduleIdentityRelease builds a real, dependency-free Go command in the
// requested namespace. Only its public build identity is used by VerifyRelease;
// the fixture is never executed.
func moduleIdentityRelease(t *testing.T, module, flagsModule string) (backupmanifest.Manifest, backupops.Release, []byte) {
	t.Helper()
	source, dir := t.TempDir(), t.TempDir()
	files := map[string]string{
		"go.mod":                     "module " + module + "\n\ngo 1.26.0\n",
		"internal/buildinfo/info.go": "package buildinfo\nvar injectedVersion = \"development\"\nvar injectedRevision = \"unknown\"\nfunc Identity() string { return injectedVersion + injectedRevision }\n",
		"cmd/gateway/main.go":        "package main\nimport (\"fmt\"; \"" + module + "/internal/buildinfo\")\nfunc main() { fmt.Print(buildinfo.Identity()) }\n",
	}
	for name, contents := range files {
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"build", "-buildvcs=false", "-o", filepath.Join(dir, "gateway")}
	revision := strings.Repeat("a", 40)
	if flagsModule != "" {
		prefix := flagsModule + "/internal/buildinfo."
		args = append(args, "-ldflags", "-X "+prefix+"injectedVersion=synthetic-v1 -X "+prefix+"injectedRevision="+revision)
	}
	args = append(args, "./cmd/gateway")
	cmd := exec.CommandContext(context.Background(), "go", args...)
	cmd.Dir = source
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=arm64", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build namespace fixture: %v %s", err, out)
	}
	bi, err := buildinfo.ReadFile(filepath.Join(dir, "gateway"))
	if err != nil || bi.Main.Path != module || bi.Path != module+"/cmd/gateway" {
		t.Fatalf("namespace fixture identity=%+v: %v", bi, err)
	}
	if err := os.Mkdir(filepath.Join(dir, "migrations"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "migrations", "000001_synthetic.sql"), []byte("SELECT 1;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	identity := backupmanifest.GatewayIdentity{Version: "synthetic-v1", Revision: revision, Artifact: &backupmanifest.SoftwareArtifact{Kind: "binary", SHA256: digestFile(t, filepath.Join(dir, "gateway")), Platform: "linux/arm64"}}
	ledger, _ := directoryLedger(t, dir)
	return backupmanifest.Manifest{SchemaVersion: 2, Gateway: identity}, backupops.Release{Directory: dir, Identity: identity}, ledger
}

// Keep the executable's section offsets and build-info string length intact
// while forging one public identity field. Blank lines pad the shorter path;
// ReadFile must still observe the requested inconsistent pair.
func patchReleaseModuleField(t *testing.T, path, field, old, replacement string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := []byte(field + "\t" + old)
	if !bytes.Contains(body, marker) || len(replacement) > len(old) {
		t.Fatal("module identity field is unavailable for the forged fixture")
	}
	// The linker retains both the build-info record and runtime's copy.
	for offset := 0; offset < len(body); {
		start := bytes.Index(body[offset:], marker)
		if start < 0 {
			break
		}
		start += offset
		end := bytes.IndexByte(body[start:], '\n')
		if end < 0 {
			t.Fatal("module identity field has no line terminator")
		}
		line := bytes.Replace(body[start:start+end+1], []byte(old), []byte(replacement), 1)
		line = append(line, bytes.Repeat([]byte{'\n'}, len(old)-len(replacement))...)
		copy(body[start:start+end+1], line)
		offset = start + end + 1
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	bi, err := buildinfo.ReadFile(path)
	if err != nil || field == "path" && bi.Path != replacement || field == "mod" && bi.Main.Path != replacement {
		t.Fatalf("forged identity was not retained: %+v %v", bi, err)
	}
}
