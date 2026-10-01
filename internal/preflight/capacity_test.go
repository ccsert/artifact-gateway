package preflight

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/capacityplan"
)

func capacityInput() string {
	now := time.Now().UTC()
	return strings.ReplaceAll(strings.ReplaceAll(`{
		"schemaVersion":1,"inventoryId":"synthetic-batch","complete":true,"targetId":"target-a",
		"references":[{"repositoryId":"repo-a","logicalKey":"blob-a","objectKey":"blob/a","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":10}],
		"snapshot":{"targetId":"target-a","observedAt":"OBSERVED","validUntil":"UNTIL","storagePoolId":"pool-a","freeBytes":100,
			"repositories":[{"repositoryId":"repo-a","usedBytes":5,"quotaBytes":100}],
			"objects":[{"key":"blob/a","state":"absent"}],"references":[{"repositoryId":"repo-a","key":"blob-a","state":"absent"}]},
		"peak":{"storagePoolId":"pool-a","downloadBytes":1,"uploadBytes":2,"backupBytes":3,"restoreBytes":4,"headroomBytes":5}
	}`, "OBSERVED", now.Add(-time.Minute).Format(time.RFC3339Nano)), "UNTIL", now.Add(time.Minute).Format(time.RFC3339Nano))
}

func runCapacityInput(t *testing.T, input string, args ...string) (int, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(input), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := RunCLI(context.Background(), append([]string{"capacity", "--input", path}, args...), &out, &errOut)
	readBack, err := os.ReadFile(path)
	if err != nil || string(readBack) != input {
		t.Fatal("preflight changed input")
	}
	return code, out.String(), errOut.String()
}

func TestCapacityCLIReturnsThreeBudgetOutcomesWithoutLoadingRuntimeConfig(t *testing.T) {
	t.Setenv("GATEWAY_DATABASE_URL", "synthetic-invalid-config-marker")
	cases := []struct {
		name, input string
		code        int
		status      capacityplan.Status
	}{
		{"sufficient", capacityInput(), 0, capacityplan.Sufficient},
		{"quota insufficient", strings.Replace(capacityInput(), `"quotaBytes":100`, `"quotaBytes":9`, 1), 1, capacityplan.Insufficient},
		{"space insufficient", strings.Replace(capacityInput(), `"freeBytes":100`, `"freeBytes":24`, 1), 1, capacityplan.Insufficient},
		{"missing space", strings.Replace(capacityInput(), `"freeBytes":100`, `"freeBytes":null`, 1), 3, capacityplan.Unknown},
		{"incomplete", strings.Replace(capacityInput(), `"complete":true`, `"complete":false`, 1), 3, capacityplan.Unknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runCapacityInput(t, tc.input)
			var report capacityplan.Report
			if code != tc.code || errOut != "" || json.Unmarshal([]byte(out), &report) != nil || report.Status != tc.status || report.SchemaVersion != 1 {
				t.Fatalf("code=%d out=%q stderr=%q", code, out, errOut)
			}
			if !strings.Contains(out, `"inputSha256":"sha256:`) || strings.Contains(out, "synthetic-invalid-config-marker") {
				t.Fatalf("input identity missing or config exposed: %s", out)
			}
		})
	}
}

func TestCapacityCLIRejectsAmbiguousInvalidOrOversizedJSONWithoutEcho(t *testing.T) {
	marker := "synthetic-sensitive-marker"
	cases := []string{
		`{"schemaVersion":` + marker,
		capacityInput() + `{}`,
		strings.Replace(capacityInput(), `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1),
		strings.Replace(capacityInput(), `"size":10`, `"size":10,"size":0`, 1),
		strings.Replace(capacityInput(), `"size":10`, `"size":10,"Size":0`, 1),
		strings.Replace(capacityInput(), `"freeBytes":100`, `"freeBytes":null,"FreeBytes":100`, 1),
		strings.Replace(capacityInput(), `"quotaBytes":100`, `"quotaBytes":9,"QUOTABYTES":100`, 1),
		strings.Replace(capacityInput(), `"key":"blob/a"`, `"key":"blob/a","Key":"other"`, 1),
		strings.Replace(capacityInput(), `"blob/a"`, "\"blob/\xff\"", 1),
		strings.Replace(capacityInput(), `"size":10`, `"size":1.5`, 1),
		strings.Replace(capacityInput(), `"size":10`, `"size":9223372036854775808`, 1),
		strings.Replace(capacityInput(), `"schemaVersion":1`, `"unexpected":"`+marker+`","schemaVersion":1`, 1),
		"null",
		strings.Repeat(" ", (8<<20)+1),
	}
	for i, input := range cases {
		code, out, errOut := runCapacityInput(t, input)
		if code != 2 || out != "" || errOut == "" || strings.Contains(errOut, marker) {
			t.Fatalf("case=%d code=%d stdout=%q stderr=%q", i, code, out, errOut)
		}
	}
}

func TestCapacityCLIUsageAndMissingFileAreSafe(t *testing.T) {
	for _, args := range [][]string{{"capacity"}, {"capacity", "--input", "/missing/synthetic-sensitive-marker"}, {"capacity", "--input", "x", "extra"}, {"capacity", "--format", "text"}} {
		var out, errOut bytes.Buffer
		code := RunCLI(context.Background(), args, &out, &errOut)
		if code != 2 || strings.Contains(errOut.String(), "synthetic-sensitive-marker") {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", args, code, out.String(), errOut.String())
		}
	}
}

type failingCapacityWriter struct{}

func (failingCapacityWriter) Write([]byte) (int, error) { return 0, context.Canceled }

func TestCapacityCLIReportsOutputFailureAndCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(capacityInput()), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := RunCLI(context.Background(), []string{"capacity", "--input", path}, failingCapacityWriter{}, &errOut); code != 2 {
		t.Fatalf("write failure code=%d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := RunCLI(ctx, []string{"capacity", "--input", path}, &out, &errOut); code != 3 {
		t.Fatalf("cancel code=%d", code)
	}
}
