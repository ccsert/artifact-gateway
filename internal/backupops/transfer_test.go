package backupops_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
	"github.com/artifact-gateway/artifact-gateway/internal/backupops"
)

type fakeSource struct {
	ledger           []byte
	objects          map[string][]byte
	changed          bool
	observations     int
	interrupted      bool
	missingReference bool
	lateInventory    bool
	walks            int
}

func (s *fakeSource) ObserveFrozen(context.Context) (string, error) {
	s.observations++
	if s.changed && s.observations > 1 {
		return "new-stop-epoch", nil
	}
	return "synthetic-stopped-epoch", nil
}
func (s *fakeSource) Dump(_ context.Context, w io.Writer) error {
	_, err := w.Write([]byte("synthetic-PG-port-not-a-real-dump"))
	return err
}
func (s *fakeSource) Ledger(context.Context) ([]byte, error) { return s.ledger, nil }
func (s *fakeSource) Metadata(context.Context) ([]byte, error) {
	return []byte("[{\"table\":\"synthetic\",\"rows\":1}]"), nil
}
func (s *fakeSource) WalkObjects(_ context.Context, visit func(string, int64) error) error {
	s.walks++
	if s.lateInventory && s.walks > 1 {
		return visit("synthetic/new", 1)
	}
	for _, key := range []string{"synthetic/a", "synthetic/b"} {
		if err := visit(key, int64(len(s.objects[key]))); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeSource) WalkReferences(_ context.Context, visit func(string) error) error {
	if s.missingReference {
		return visit("synthetic/missing")
	}
	return visit("synthetic/a")
}
func (s *fakeSource) Open(_ context.Context, key string) (io.ReadCloser, int64, error) {
	body := s.objects[key]
	if s.interrupted {
		return io.NopCloser(&brokenReader{}), int64(len(body)), nil
	}
	return io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
}

type brokenReader struct{}

func (*brokenReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic-sensitive-upstream-error")
}

type fakeTarget struct {
	ledger    []byte
	objects   map[string][]byte
	nonempty  bool
	writes    int
	wrongRead bool
}

func (t *fakeTarget) CheckEmpty(context.Context) error {
	if t.nonempty {
		return errors.New("foreign data")
	}
	return nil
}
func (t *fakeTarget) RestoreDatabase(_ context.Context, r io.Reader) error {
	t.writes++
	_, err := io.Copy(io.Discard, r)
	return err
}
func (t *fakeTarget) Ledger(context.Context) ([]byte, error) { return t.ledger, nil }
func (t *fakeTarget) Metadata(context.Context) ([]byte, error) {
	return []byte("[{\"table\":\"synthetic\",\"rows\":1}]"), nil
}
func (t *fakeTarget) Put(_ context.Context, key string, r io.ReadSeeker, _ int64, _ string) error {
	t.writes++
	body, err := io.ReadAll(r)
	t.objects[key] = body
	return err
}
func (t *fakeTarget) Open(_ context.Context, key string) (io.ReadCloser, int64, error) {
	body := t.objects[key]
	if t.wrongRead {
		body = []byte("same length wrong bytes")
	}
	return io.NopCloser(bytes.NewReader(body)), int64(len(body)), nil
}

func TestActualTransferPortsAndSafeFailureBoundaries(t *testing.T) {
	m, release, ledger := binaryRelease(t)
	_ = m
	scope := backupmanifest.WriterEvidence{ScopeID: "synthetic-scope", InventoryDeclaredComplete: true, Writers: []backupmanifest.Writer{{ID: "synthetic-writer"}}}
	for _, scenario := range []string{"roundtrip", "interrupted export", "writer restarted", "existing bundle", "missing reference", "inventory changed", "nonempty target", "object corruption", "wrong readback", "wrong release"} {
		t.Run(scenario, func(t *testing.T) {
			source := &fakeSource{ledger: ledger, objects: map[string][]byte{"synthetic/a": []byte("synthetic-object-marker"), "synthetic/b": {}}}
			directory := filepath.Join(t.TempDir(), "bundle")
			if scenario == "interrupted export" {
				source.interrupted = true
			}
			if scenario == "writer restarted" {
				source.changed = true
			}
			source.missingReference = scenario == "missing reference"
			source.lateInventory = scenario == "inventory changed"
			if scenario == "existing bundle" {
				_ = os.Mkdir(directory, 0700)
				_ = os.WriteFile(filepath.Join(directory, "sentinel"), []byte("protected"), 0600)
			}
			report, err := backupops.Export(context.Background(), source, directory, release, scope, "synthetic-backup")
			if scenario == "interrupted export" || scenario == "writer restarted" || scenario == "existing bundle" || scenario == "missing reference" || scenario == "inventory changed" {
				if err == nil || report.Status != "failed" {
					t.Fatalf("unexpected success %+v", report)
				}
				if _, err = os.Stat(filepath.Join(directory, "manifest.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("half backup complete")
				}
				if strings.Contains(report.Reason, "sensitive") {
					t.Fatal("raw error leaked")
				}
				if scenario == "interrupted export" || scenario == "missing reference" {
					path := filepath.Join(directory, "failure.json")
					data, e := os.ReadFile(path)
					if e != nil || strings.Contains(string(data), "synthetic-sensitive-upstream-error") {
						t.Fatal("private failure evidence unavailable or raw upstream error retained")
					}
					var receipt struct{ BackupID, Stage, Key, File, Reason string }
					if json.Unmarshal(data, &receipt) != nil || receipt.BackupID != "synthetic-backup" || receipt.Reason != report.Reason {
						t.Fatal("private failure receipt invalid")
					}
					if scenario == "interrupted export" && (receipt.Stage != "object_export" || receipt.Key != "synthetic/a" || receipt.File != "objects/000000000000.data") {
						t.Fatal("interrupted object cannot be located")
					}
					if scenario == "missing reference" && (receipt.Stage != "source_object_reference_recheck" || receipt.Key != "synthetic/missing") {
						t.Fatal("missing published object cannot be located")
					}
					info, e := os.Stat(path)
					if e != nil || info.Mode().Perm() != 0600 {
						t.Fatal("failure receipt is not private")
					}
				}
				return
			}
			if err != nil || report.Status != "exported" || report.VerifiedObjects != 2 || report.Consistency == "verified" {
				t.Fatalf("export %+v %v", report, err)
			}
			target := &fakeTarget{ledger: ledger, objects: map[string][]byte{}}
			candidate := release
			if scenario == "nonempty target" {
				target.nonempty = true
			}
			if scenario == "object corruption" {
				_ = os.WriteFile(filepath.Join(directory, "objects", "000000000000.data"), []byte("corrupt"), 0600)
			}
			if scenario == "wrong readback" {
				target.wrongRead = true
			}
			if scenario == "wrong release" {
				candidate.Identity.Version = "synthetic-other"
			}
			result, err := backupops.Restore(context.Background(), directory, candidate, target)
			if scenario != "roundtrip" {
				if err == nil || result.Status != "failed" {
					t.Fatalf("unexpected restore success %+v", result)
				}
				if scenario != "wrong readback" && target.writes != 0 {
					t.Fatal("validation wrote to target")
				}
				return
			}
			if err != nil || result.Status != "restored" || result.Metadata != "verified" || result.Objects != "verified" || !bytes.Equal(target.objects["synthetic/a"], source.objects["synthetic/a"]) {
				t.Fatalf("restore %+v %v", result, err)
			}
		})
	}
}

func TestCLIRequiresExplicitPrivateSpecAndDoesNotEchoSecrets(t *testing.T) {
	for _, args := range [][]string{nil, {"restore"}, {"export", "--bundle", "synthetic"}, {"restore", "--spec", "synthetic-secret-marker", "--bundle", "synthetic"}, {"unknown"}} {
		var out, errOut bytes.Buffer
		if code := backupops.RunCLI(context.Background(), args, &out, &errOut); code != 2 {
			t.Fatalf("code=%d", code)
		}
		if strings.Contains(out.String()+errOut.String(), "synthetic-secret-marker") {
			t.Fatal("spec path leaked")
		}
	}
}
