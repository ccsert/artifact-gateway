package backupops_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/artifact-gateway/artifact-gateway/internal/backupops"
)

func binaryRelease(t *testing.T) (backupmanifest.Manifest, backupops.Release, []byte) {
	t.Helper()
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	revision := strings.Repeat("a", 40)
	flags := "-X github.com/artifact-gateway/artifact-gateway/internal/buildinfo.injectedVersion=synthetic-v1 -X github.com/artifact-gateway/artifact-gateway/internal/buildinfo.injectedRevision=" + revision
	cmd := exec.CommandContext(context.Background(), "go", "build", "-buildvcs=false", "-ldflags", flags, "-o", filepath.Join(dir, "gateway"), "./cmd/gateway")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=arm64")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build synthetic release: %v %s", err, out)
	}
	body, err := os.ReadFile(filepath.Join(dir, "gateway"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	identity := backupmanifest.GatewayIdentity{Version: "synthetic-v1", Revision: revision, Artifact: &backupmanifest.SoftwareArtifact{Kind: "binary", SHA256: "sha256:" + hex.EncodeToString(sum[:]), Platform: "linux/arm64"}}
	_ = os.Mkdir(filepath.Join(dir, "migrations"), 0700)
	sql := []byte("SELECT 1;\n")
	_ = os.WriteFile(filepath.Join(dir, "migrations", "000001_synthetic.sql"), sql, 0600)
	h := sha256.Sum256(sql)
	ledger := []byte("filename\tsha256\n000001_synthetic.sql\t" + hex.EncodeToString(h[:]) + "\n")
	return backupmanifest.Manifest{SchemaVersion: 2, Gateway: identity}, backupops.Release{Directory: dir, Identity: identity}, ledger
}

func TestActualBinaryAndAppliedSchemaMustMatch(t *testing.T) {
	m, release, ledger := binaryRelease(t)
	if err := backupops.VerifyRelease(m, release, bytes.NewReader(ledger)); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"version", "revision", "platform", "digest", "migration", "extra migration"} {
		t.Run(field, func(t *testing.T) {
			wrong := m
			copyIdentity := *m.Gateway.Artifact
			wrong.Gateway.Artifact = &copyIdentity
			candidate := release
			altered := ledger
			switch field {
			case "version":
				wrong.Gateway.Version = "synthetic-v2"
				candidate.Identity = wrong.Gateway
			case "revision":
				wrong.Gateway.Revision = strings.Repeat("b", 40)
				candidate.Identity = wrong.Gateway
			case "platform":
				wrong.Gateway.Artifact.Platform = "linux/amd64"
				candidate.Identity = wrong.Gateway
			case "digest":
				wrong.Gateway.Artifact.SHA256 = "sha256:" + strings.Repeat("0", 64)
				candidate.Identity = wrong.Gateway
			case "migration":
				altered = []byte("filename\tsha256\n000001_synthetic.sql\t" + strings.Repeat("0", 64) + "\n")
			case "extra migration":
				altered = append(append([]byte(nil), ledger...), []byte("000002_extra.sql\t"+strings.Repeat("0", 64)+"\n")...)
			}
			if backupops.VerifyRelease(wrong, candidate, bytes.NewReader(altered)) == nil {
				t.Fatal("wrong observed identity accepted")
			}
		})
	}
}
