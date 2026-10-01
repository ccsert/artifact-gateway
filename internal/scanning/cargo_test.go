package scanning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func TestCargoCrateScanAcceptsInternalFormatAndVerifiesBytes(t *testing.T) {
	content := []byte("crate archive")
	sum := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	artifact := Artifact{RepositoryID: "cargo-repo", Format: repository.FormatCargo,
		Coordinate: "demo@1.0.0", Digest: digest, Assets: []Asset{{Path: "demo-1.0.0.crate",
		Digest: digest, Size: int64(len(content)), MediaType: "application/gzip",
		Open: func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(content)), nil }}}}
	if err := validateArtifact(artifact, 128<<20); err != nil {
		t.Fatalf("Cargo scan rejected: %v", err)
	}
	if err := copyVerified(io.Discard, bytes.NewReader(content), int64(len(content)), digest); err != nil {
		t.Fatalf("crate checksum rejected: %v", err)
	}
	if err := copyVerified(io.Discard, bytes.NewReader([]byte("changed")), 7, digest); err != ErrAssetIntegrity {
		t.Fatalf("changed crate accepted: %v", err)
	}
}
