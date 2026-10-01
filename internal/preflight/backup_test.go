package preflight

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/backupmanifest"
)

func backupInput(t *testing.T) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) backupmanifest.File {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		size := int64(len(body))
		sum := sha256.Sum256([]byte(body))
		return backupmanifest.File{Path: name, Size: &size, SHA256: "sha256:" + hex.EncodeToString(sum[:])}
	}
	zero := int64(0)
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	m := backupmanifest.Manifest{
		SchemaVersion: 1, BackupID: "synthetic-backup", State: "complete", StartedAt: start, CompletedAt: start.Add(time.Minute),
		Gateway:  backupmanifest.GatewayIdentity{Version: "synthetic-v1", Revision: strings.Repeat("a", 40), ImageDigest: "sha256:" + strings.Repeat("b", 64)},
		Database: backupmanifest.DatabaseExport{Format: "pg-custom-v1", File: write("database.dump", "synthetic-not-a-restorable-dump")},
		Schema:   backupmanifest.SchemaExport{Format: "gateway-migrations-tsv-v1", File: write("migrations.tsv", "filename\tsha256\n000000_synthetic.sql\t"+strings.Repeat("c", 64)+"\n")},
		Objects:  backupmanifest.ObjectExport{Format: "s3-bytes-v1", Inventory: write("objects.jsonl", ""), Count: &zero, Bytes: &zero},
		Writers:  backupmanifest.WriterEvidence{ScopeID: "synthetic-scope", InventoryDeclaredComplete: true, Writers: []backupmanifest.Writer{{ID: "synthetic-api", StopConfirmed: true, StoppedAt: start, ConfirmedThrough: start.Add(time.Minute)}}},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestBackupCLISeparatesVerifiedBytesFromUnknownConsistency(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "synthetic-invalid-config-marker")
	path, data := backupInput(t)
	var out, errOut bytes.Buffer
	code := RunCLI(context.Background(), []string{"backup", "--input", path, "--format", "json"}, &out, &errOut)
	var report backupmanifest.Report
	sum := sha256.Sum256(data)
	if code != 3 || errOut.Len() != 0 || json.Unmarshal(out.Bytes(), &report) != nil || report.Status != backupmanifest.Unknown || report.Integrity != backupmanifest.Verified || report.Consistency != backupmanifest.Unknown || report.WriterEvidence != "declared" || report.ManifestSHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &errOut)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, data) {
		t.Fatal("preflight changed its input")
	}
	if strings.Contains(out.String()+errOut.String(), "synthetic-invalid-config-marker") {
		t.Fatal("runtime configuration loaded or echoed")
	}
}

func TestBackupCLICorruptionAndUnknownProfileRemainNonSuccessful(t *testing.T) {
	for _, corrupt := range []bool{true, false} {
		path, data := backupInput(t)
		want := 3
		if corrupt {
			want = 1
			if err := os.WriteFile(filepath.Join(filepath.Dir(path), "database.dump"), []byte("interrupted"), 0600); err != nil {
				t.Fatal(err)
			}
		} else {
			data = bytes.Replace(data, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":2`), 1)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		var out, errOut bytes.Buffer
		if code := RunCLI(context.Background(), []string{"backup", "--input", path}, &out, &errOut); code != want || errOut.Len() != 0 || !strings.Contains(out.String(), `"consistency":"unknown"`) {
			t.Fatalf("code=%d out=%s stderr=%s", code, &out, &errOut)
		}
	}
}

func TestBackupCLIRejectsAmbiguousInputWithoutEcho(t *testing.T) {
	path, data := backupInput(t)
	marker := "synthetic-sensitive-marker"
	valid := string(data)
	cases := []string{
		`{"schemaVersion":` + marker,
		valid + `{}`,
		strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":1,"SchemaVersion":1`, 1),
		strings.Replace(valid, `"count":0`, `"count":null,"COUNT":0`, 1),
		strings.Replace(valid, `"size":31`, `"size":31,"Size":0`, 1),
		strings.Replace(valid, `"backupId"`, `"bacKupId"`, 1),
		strings.Replace(valid, `"scopeId"`, `"unexpected"`, 1),
		strings.Replace(valid, `"count":0`, `"count":1.5`, 1),
		strings.Replace(valid, `"count":0`, `"count":9223372036854775808`, 1),
		"null", strings.Repeat(" ", (8<<20)+1),
	}
	for i, input := range cases {
		if input == valid {
			t.Fatalf("case %d did not change fixture", i)
		}
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		if code := RunCLI(context.Background(), []string{"backup", "--input", path}, &out, &errOut); code != 2 || out.Len() != 0 || errOut.Len() == 0 || strings.Contains(errOut.String(), marker) {
			t.Fatalf("case=%d code=%d out=%s stderr=%s", i, code, &out, &errOut)
		}
	}
}

func TestBackupCLIPrivateInputsUsageCancellationAndReportFailure(t *testing.T) {
	path, _ := backupInput(t)
	for _, args := range [][]string{{"backup"}, {"backup", "--input", "/missing/synthetic-sensitive-marker"}, {"backup", "--input", path, "extra"}, {"backup", "--input", path, "--format", "text"}, {"backup", "--unknown"}} {
		var out, errOut bytes.Buffer
		if code := RunCLI(context.Background(), args, &out, &errOut); code != 2 || strings.Contains(errOut.String(), "synthetic-sensitive-marker") {
			t.Fatalf("code=%d stderr=%s", code, &errOut)
		}
	}
	var out, errOut bytes.Buffer
	if code := RunCLI(context.Background(), []string{"backup", "--help"}, &out, &errOut); code != 0 {
		t.Fatal(code)
	}
	if code := RunCLI(context.Background(), []string{"backup", "--input", path}, failingCapacityWriter{}, &errOut); code != 2 {
		t.Fatal(code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out.Reset()
	if code := RunCLI(ctx, []string{"backup", "--input", path}, &out, &errOut); code != 3 || !strings.Contains(out.String(), "cancelled") {
		t.Fatal(code, out.String())
	}
	for _, target := range []string{path, filepath.Dir(path)} {
		t.Run("public mode "+filepath.Base(target), func(t *testing.T) {
			if err := os.Chmod(target, 0755); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := os.Chmod(target, 0700); err != nil {
					t.Fatal(err)
				}
			}()
			out.Reset()
			errOut.Reset()
			if code := RunCLI(context.Background(), []string{"backup", "--input", path}, &out, &errOut); code != 2 {
				t.Fatalf("public input code=%d out=%s", code, &out)
			}
		})
	}
	symlink := filepath.Join(filepath.Dir(path), "synthetic-sensitive-marker")
	if err := os.Symlink(path, symlink); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := RunCLI(context.Background(), []string{"backup", "--input", symlink}, &out, &errOut); code != 2 || strings.Contains(errOut.String(), "synthetic-sensitive-marker") {
		t.Fatal(code, errOut.String())
	}
}

func TestBackupCLIUnknownVersionDoesNotApplyV1BodySchema(t *testing.T) {
	path, data := backupInput(t)
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["schemaVersion"] = 2
	manifest["futureField"] = map[string]any{"synthetic": "future"}
	manifest["database"] = "future-profile-without-v1-structure"
	manifest["startedAt"] = "future-time-representation"
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	var report backupmanifest.Report
	code := RunCLI(context.Background(), []string{"backup", "--input", path}, &out, &errOut)
	if code != 3 || errOut.Len() != 0 || json.Unmarshal(out.Bytes(), &report) != nil || report.Status != backupmanifest.Unknown || report.Integrity == backupmanifest.Verified || !strings.Contains(out.String(), "manifest_version_unsupported") {
		t.Fatalf("code=%d out=%s stderr=%s", code, &out, &errOut)
	}
	if report.ManifestSHA256 == "" {
		t.Fatal("missing evidence identity")
	}
}

func TestBackupCLINegativeObjectTotalsAreInvalid(t *testing.T) {
	for _, field := range []string{"count", "bytes"} {
		path, data := backupInput(t)
		input := bytes.Replace(data, []byte(`"`+field+`":0`), []byte(`"`+field+`":-1`), 1)
		if bytes.Equal(input, data) {
			t.Fatal("fixture unchanged")
		}
		if err := os.WriteFile(path, input, 0600); err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		if code := RunCLI(context.Background(), []string{"backup", "--input", path}, &out, &errOut); code != 1 || errOut.Len() != 0 || !strings.Contains(out.String(), `"status":"invalid"`) {
			t.Fatalf("field=%s code=%d out=%s stderr=%s", field, code, &out, &errOut)
		}
	}
}

func TestBackupCLIAcceptsUnambiguousASCIIFieldCaseVariants(t *testing.T) {
	path, data := backupInput(t)
	for _, field := range []string{"schemaVersion", "scopeId", "stopConfirmed"} {
		data = bytes.Replace(data, []byte(`"`+field+`"`), []byte(`"`+strings.ToUpper(field)+`"`), 1)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := RunCLI(context.Background(), []string{"backup", "--input", path}, &out, &errOut); code != 3 || errOut.Len() != 0 || !strings.Contains(out.String(), `"integrity":"verified"`) {
		t.Fatalf("code=%d out=%s stderr=%s", code, &out, &errOut)
	}
}
