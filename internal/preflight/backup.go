package preflight

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/artifact-gateway/artifact-gateway/internal/opsjson"
)

const maxBackupInputBytes = 8 << 20

func runBackup(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("preflight backup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("input", "", "private local backup manifest")
	format := flags.String("format", "json", "output format: json")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(stdout, "usage: gateway preflight backup --input manifest.json [--format json]")
			return 0
		}
		_, _ = fmt.Fprintln(stderr, "invalid backup preflight arguments")
		return 2
	}
	if *input == "" || *format != "json" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "backup preflight requires --input and JSON output, without positional arguments")
		return 2
	}
	if ctx.Err() != nil {
		return writeBackupReport(stdout, stderr, backupmanifest.Report{SchemaVersion: 1, CheckedAt: time.Now().UTC(), Status: backupmanifest.Unknown, Integrity: backupmanifest.Unknown, Consistency: backupmanifest.Unknown, WriterEvidence: "unknown", Reasons: []string{"cancelled", "consistency_not_independently_verified"}})
	}
	absolute, err := filepath.Abs(*input)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backup input is unavailable")
		return 2
	}
	directory, name := filepath.Dir(absolute), filepath.Base(absolute)
	data, err := readBackupInput(directory, name)
	if err != nil {
		// Paths, parser excerpts, keys, and lower-level IO errors stay private.
		_, _ = fmt.Fprintln(stderr, "backup input is unavailable, not private, or exceeds its regular-file limit")
		return 2
	}
	// Read the version envelope without applying the v1 body schema to a future
	// version. Common JSON syntax/ambiguity limits still apply to all input.
	var envelope map[string]json.RawMessage
	if opsjson.Decode(data, &envelope) != nil {
		_, _ = fmt.Fprintln(stderr, "backup input must be one valid, unambiguous versioned JSON manifest")
		return 2
	}
	var manifest backupmanifest.Manifest
	for name, value := range envelope {
		if strings.EqualFold(name, "schemaVersion") && json.Unmarshal(value, &manifest.SchemaVersion) != nil {
			_, _ = fmt.Fprintln(stderr, "backup input requires an integer schema version")
			return 2
		}
	}
	if manifest.SchemaVersion == 1 && opsjson.Decode(data, &manifest) != nil {
		_, _ = fmt.Fprintln(stderr, "backup input must be one valid, unambiguous versioned JSON manifest")
		return 2
	}
	report := backupmanifest.Verify(ctx, directory, manifest)
	sum := sha256.Sum256(data)
	report.ManifestSHA256 = "sha256:" + hex.EncodeToString(sum[:])
	return writeBackupReport(stdout, stderr, report)
}

func readBackupInput(directory, name string) ([]byte, error) {
	invalid := errors.New("invalid backup input")
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, invalid
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, invalid
	}
	info, err = root.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxBackupInputBytes {
		return nil, invalid
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, invalid
	}
	defer func() { _ = file.Close() }()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxBackupInputBytes {
		return nil, invalid
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBackupInputBytes+1))
	if err != nil || len(data) > maxBackupInputBytes {
		return nil, invalid
	}
	return data, nil
}

func writeBackupReport(stdout, stderr io.Writer, report backupmanifest.Report) int {
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		_, _ = fmt.Fprintln(stderr, "backup report could not be written")
		return 2
	}
	if report.Status == backupmanifest.Invalid {
		return 1
	}
	// Byte verification never grants overall backup success in this slice.
	return 3
}
