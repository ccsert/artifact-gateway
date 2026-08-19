package consoletheme

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIValidatesInstallsAndListsTheme(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(t.TempDir(), "customer.theme.json")
	data := `{"$schema":"https://artifact-gateway.local/schemas/console-theme-v1.json","schemaVersion":1,"id":"customer-blue","name":"Customer Blue","mode":"light","token":{"colorPrimary":"#123456","colorSuccess":"#17803d","colorWarning":"#a15c00","colorError":"#b42318","colorInfo":"#026aa2","colorTextBase":"#101828","colorBgBase":"#ffffff"}}`
	if err := os.WriteFile(source, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := RunCLI(context.Background(), []string{"validate", "--file", source, "--format", "json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("validate code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"status": "valid"`) {
		t.Fatalf("validate output = %s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := RunCLI(context.Background(), []string{"install", "--file", source, "--dir", directory, "--format", "json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("install code = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(directory, "customer-blue.theme.json")); err != nil {
		t.Fatalf("installed file: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := RunCLI(context.Background(), []string{"list", "--dir", directory, "--format", "json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"id": "customer-blue"`) {
		t.Fatalf("list output = %s", stdout.String())
	}
}
