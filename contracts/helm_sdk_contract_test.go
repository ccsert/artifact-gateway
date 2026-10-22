package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelmGeneratedConsoleClientAcceptsNamedBinaryFiles(t *testing.T) {
	generated, err := os.ReadFile(filepath.Join("..", "console", "src", "client", "types.gen.ts"))
	if err != nil {
		t.Fatal(err)
	}
	types := string(generated)
	start := strings.Index(types, "export type PublishHelmPreviewChartData")
	if start < 0 {
		t.Fatal("generated Helm upload type is missing")
	}
	end := strings.Index(types[start:], "export type PublishHelmPreviewChartErrors")
	if end < 0 {
		t.Fatal("generated Helm upload boundary is missing")
	}
	upload := types[start : start+end]
	for _, field := range []string{"chart: Blob | File", "provenance?: Blob | File"} {
		if !strings.Contains(upload, field) {
			t.Fatalf("generated Helm upload must accept named binary files for %s: %s", field, upload)
		}
	}
}
