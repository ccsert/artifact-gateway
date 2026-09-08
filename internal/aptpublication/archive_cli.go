package aptpublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// RunArchiveCLI verifies a local archive without loading server configuration,
// connecting to a database, extracting paths, or contacting a signer.
func RunArchiveCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "verify" || args[1] == "" {
		_, _ = fmt.Fprintln(stderr, "usage: gateway apt-snapshot verify <archive.tar|->")
		return 2
	}
	input := stdin
	if args[1] != "-" {
		file, err := os.Open(args[1])
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "open APT snapshot archive: %v\n", err)
			return 1
		}
		defer func() { _ = file.Close() }()
		input = file
	}
	hash := sha256.New()
	manifest, err := VerifySnapshotArchive(ctx, io.TeeReader(input, hash))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "verify APT snapshot archive: %v\n", err)
		return 1
	}
	result := struct {
		Integrity     string `json:"integrity"`
		ArchiveDigest string `json:"archiveDigest"`
		Signatures    string `json:"signatures"`
		SnapshotID    string `json:"snapshotId"`
		RepositoryID  string `json:"repositoryId"`
		Packages      int    `json:"packages"`
		Assets        int    `json:"assets"`
	}{"verified", "sha256:" + hex.EncodeToString(hash.Sum(nil)), "not_checked", manifest.Snapshot.ID, manifest.Snapshot.RepositoryID, len(manifest.Packages), len(manifest.Assets)}
	if err = json.NewEncoder(stdout).Encode(result); err != nil {
		_, _ = fmt.Fprintf(stderr, "write APT snapshot verification result: %v\n", err)
		return 1
	}
	return 0
}
