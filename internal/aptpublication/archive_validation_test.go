package aptpublication

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/objectstore"
	"github.com/artifact-gateway/artifact-gateway/internal/repository"
)

func archiveValidationFixture(t *testing.T) (SnapshotArchiveExporter, string, []byte) {
	t.Helper()
	ctx := context.Background()
	store := repository.NewMemoryStore()
	repo := createAPTHostedRepository(t, ctx, store)
	objects := objectstore.NewMemoryStore()
	session := stageAPTPackage(t, ctx, store, objects, repo.ID, "archive-validation", "widget", "1.0-1")
	snapshot, err := NewPublisher(store, objects, deterministicAPTSigner{}).Publish(ctx, PublishSnapshotInput{
		ID: "00000000-0000-4000-8000-000000000048", RepositoryID: repo.ID, Suite: "stable", Sequence: 1,
		SessionIDs: []string{session.ID}, Actor: "operator", CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	exporter := SnapshotArchiveExporter{Store: store, Objects: objects}
	var output bytes.Buffer
	if _, err = exporter.Export(ctx, snapshot.ID, &output); err != nil {
		t.Fatal(err)
	}
	return exporter, snapshot.ID, output.Bytes()
}

func TestSnapshotArchiveRejectsTruncationTrailingDataAndInvalidEntries(t *testing.T) {
	t.Parallel()
	_, _, valid := archiveValidationFixture(t)
	for name, body := range map[string][]byte{
		"missing terminator":   valid[:len(valid)-1024],
		"short terminator":     valid[:len(valid)-1],
		"trailing bytes":       append(append([]byte(nil), valid...), []byte("payload")...),
		"concatenated archive": append(append([]byte(nil), valid...), valid...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifySnapshotArchive(context.Background(), bytes.NewReader(body)); !errors.Is(err, ErrSnapshotArchiveCorrupt) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	for _, kind := range []string{"symlink", "unexpected", "duplicate", "tampered"} {
		t.Run(kind, func(t *testing.T) {
			reader := tar.NewReader(bytes.NewReader(valid))
			var output bytes.Buffer
			writer := tar.NewWriter(&output)
			mutated := false
			for {
				header, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				if header.Name != "manifest.json" && !mutated {
					mutated = true
					switch kind {
					case "symlink":
						header.Typeflag = tar.TypeSymlink
						header.Linkname = "/etc/passwd"
						header.Size = 0
						body = nil
					case "unexpected":
						header.Name = "../unexpected"
					case "tampered":
						body[0] ^= 0xff
					case "duplicate":
						if err = writer.WriteHeader(header); err != nil {
							t.Fatal(err)
						}
						if _, err = writer.Write(body); err != nil {
							t.Fatal(err)
						}
					}
				}
				if err = writer.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				if _, err = writer.Write(body); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifySnapshotArchive(context.Background(), bytes.NewReader(output.Bytes())); !errors.Is(err, ErrSnapshotArchiveCorrupt) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSnapshotArchiveRejectsInconsistentManifest(t *testing.T) {
	t.Parallel()
	_, _, valid := archiveValidationFixture(t)
	for name, change := range map[string]func(*SnapshotArchiveManifest){
		"unknown version":   func(m *SnapshotArchiveManifest) { m.SchemaVersion = "future" },
		"building snapshot": func(m *SnapshotArchiveManifest) { m.Snapshot.State = repository.APTRepositorySnapshotBuilding },
		"oversize signature": func(m *SnapshotArchiveManifest) {
			for i := range m.Assets {
				if strings.HasSuffix(m.Assets[i].Path, "/InRelease") {
					m.Assets[i].Size = 17 << 20
				}
			}
		},
		"missing membership":   func(m *SnapshotArchiveManifest) { m.Packages = nil },
		"duplicate membership": func(m *SnapshotArchiveManifest) { m.Packages = append(m.Packages, m.Packages[0]) },
		"pool package absent from membership": func(m *SnapshotArchiveManifest) {
			for _, asset := range m.Assets {
				if strings.HasPrefix(asset.Path, "pool/") {
					asset.Path = repository.APTPoolPath("main", "ghost", "ghost_1.0-1_amd64.deb")
					m.Assets = append(m.Assets, asset)
					return
				}
			}
		},
		"membership digest differs from pool": func(m *SnapshotArchiveManifest) {
			m.Packages[0].Digest = "sha256:" + strings.Repeat("f", 64)
			m.Packages[0].Object = "objects/sha256/" + strings.Repeat("f", 64)
		},
		"wrong package identity": func(m *SnapshotArchiveManifest) {
			m.Packages[0].Version = "9.0-1"
			m.Packages[0].CanonicalIdentity = "widget@9.0-1#amd64"
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := rewriteSnapshotArchive(t, valid, func(name string, body []byte) ([]byte, bool) {
				if name != "manifest.json" {
					return body, true
				}
				var manifest SnapshotArchiveManifest
				if err := json.Unmarshal(body, &manifest); err != nil {
					t.Fatal(err)
				}
				change(&manifest)
				body, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				return body, true
			})
			if _, err := VerifySnapshotArchive(context.Background(), bytes.NewReader(body)); !errors.Is(err, ErrSnapshotArchiveCorrupt) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestSnapshotArchiveExporterRejectsMissingBytesBeforeWriting(t *testing.T) {
	t.Parallel()
	exporter, id, _ := archiveValidationFixture(t)
	assets, err := exporter.Store.ListAPTSnapshotAssets(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err = exporter.Objects.Delete(context.Background(), assets[0].ObjectKey); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err = exporter.Export(context.Background(), id, &output); !errors.Is(err, ErrSnapshotArchiveCorrupt) || output.Len() != 0 {
		t.Fatalf("error=%v bytes=%d", err, output.Len())
	}
}

func TestArchiveCLIReportsIntegrityWithoutClaimingSignatureTrust(t *testing.T) {
	t.Parallel()
	_, id, body := archiveValidationFixture(t)
	path := filepath.Join(t.TempDir(), "snapshot.tar")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{path, "-"} {
		var stdout, stderr bytes.Buffer
		code := RunArchiveCLI(context.Background(), []string{"verify", source}, bytes.NewReader(body), &stdout, &stderr)
		var result struct{ Integrity, Signatures, SnapshotID string }
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if code != 0 || result.Integrity != "verified" || result.Signatures != "not_checked" || result.SnapshotID != id || stderr.Len() != 0 {
			t.Fatalf("code=%d result=%#v stderr=%s", code, result, stderr.String())
		}
	}
	for _, tc := range []struct {
		args  []string
		input []byte
		want  int
	}{
		{[]string{"verify", "-"}, body[:len(body)-1], 1},
		{[]string{"verify", path + "-missing"}, nil, 1},
		{[]string{"restore", path}, nil, 2},
	} {
		var stdout, stderr bytes.Buffer
		if code := RunArchiveCLI(context.Background(), tc.args, bytes.NewReader(tc.input), &stdout, &stderr); code != tc.want || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := VerifySnapshotArchive(ctx, bytes.NewReader(body)); err == nil {
		t.Fatal("accepted canceled verification")
	}
}
