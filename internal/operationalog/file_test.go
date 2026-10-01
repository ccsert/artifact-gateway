package operationalog

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func fileOptions(t *testing.T) FileOptions {
	t.Helper()
	return FileOptions{Directory: t.TempDir(), MaxSizeBytes: 1024, MaxBackups: 10, MaxAge: 7 * 24 * time.Hour}
}

func managedFiles(t *testing.T, directory string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(directory, "gateway-*", "*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func closeFileOutput(t *testing.T, output *Output) {
	t.Helper()
	if err := output.Close(); err != nil {
		t.Error(err)
	}
}

func TestFileOutputPersistsRedactedJSONAndUsesPrivateFiles(t *testing.T) {
	options := fileOptions(t)
	output, err := NewFileOutput(options)
	if err != nil {
		t.Fatal(err)
	}
	var expected bytes.Buffer
	for _, writer := range []io.Writer{output, &expected} {
		logger := NewLogger(writer, "synthetic-instance", "synthetic-session")
		record := slog.NewRecord(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), slog.LevelInfo, "synthetic event", 0)
		record.AddAttrs(slog.String("token", "synthetic-token-marker"))
		if err := logger.WithGroup("CreDentials").With("nested", "synthetic-sensitive-marker").Handler().Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
		record = slog.NewRecord(record.Time, slog.LevelInfo, "safe event", 0)
		record.AddAttrs(slog.String("component", "synthetic-component"), slog.Int("count", 2))
		if err := logger.Handler().Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	if err := output.Flush(); err != nil {
		t.Fatal(err)
	}
	files := managedFiles(t, options.Directory)
	if len(files) != 1 {
		t.Fatalf("files = %v", files)
	}
	data := readFile(t, files[0])
	if !bytes.Equal(data, expected.Bytes()) || bytes.Contains(data, []byte("synthetic-sensitive-marker")) || bytes.Contains(data, []byte("synthetic-token-marker")) || !bytes.Contains(data, []byte(`"count":2`)) || !bytes.Contains(data, []byte(`"nested":"[redacted]"`)) {
		t.Fatalf("unexpected persisted synthetic events: %s", data)
	}
	for path, want := range map[string]os.FileMode{files[0]: 0o600, filepath.Dir(files[0]): 0o700} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("permissions for %s = %v, %v", path, info, err)
		}
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("{}\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after close = %v", err)
	}
}

func TestFileOutputRotatesOnlyBeforeCompleteEventsAtSizeBoundary(t *testing.T) {
	options := fileOptions(t)
	options.MaxSizeBytes = 18
	output, err := NewFileOutput(options)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFileOutput(t, output)
	for _, event := range []string{"{\"id\":1}\n", "{\"id\":2}\n", "{\"id\":3}\n"} {
		if n, err := output.Write([]byte(event)); err != nil || n != len(event) {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	files := managedFiles(t, options.Directory)
	if len(files) != 2 || string(readFile(t, files[0])) != "{\"id\":1}\n{\"id\":2}\n" || string(readFile(t, files[1])) != "{\"id\":3}\n" {
		t.Fatalf("rotation boundary: %v", files)
	}
}

func TestFileOutputRejectsMalformedFragmentedAndOversizedEvents(t *testing.T) {
	options := fileOptions(t)
	options.MaxSizeBytes = 20
	output, err := NewFileOutput(options)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFileOutput(t, output)
	for _, event := range []string{"{}", "{}\n{}\n", "{\n}\n", "[]\n", "bad\n", "{\"x\":\"synthetic-marker\"}\n"} {
		if n, err := output.Write([]byte(event)); n != 0 || err == nil {
			t.Fatalf("accepted invalid event %q: %d, %v", event, n, err)
		}
	}
	if n, err := output.Write(nil); n != 0 || err != nil {
		t.Fatalf("empty write = %d, %v", n, err)
	}
	if n, err := output.Write([]byte("{}\n")); n != 3 || err != nil {
		t.Fatalf("valid event after rejected events = %d, %v", n, err)
	}
	files := managedFiles(t, options.Directory)
	if len(files) != 1 || string(readFile(t, files[0])) != "{}\n" {
		t.Fatalf("rejected event modified files: %v", files)
	}
}

func TestFileOutputRetentionOnlyRemovesOwnSealedSegments(t *testing.T) {
	options := fileOptions(t)
	options.MaxSizeBytes, options.MaxBackups, options.MaxAge = 3, 1, time.Hour
	foreign := filepath.Join(options.Directory, "user-existing.ndjson")
	if err := os.WriteFile(foreign, []byte("synthetic-existing-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldOutput, err := NewFileOutput(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oldOutput.Write([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := oldOutput.Close(); err != nil {
		t.Fatal(err)
	}
	oldFile := managedFiles(t, options.Directory)[0]
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	operations := defaultFileOperations()
	operations.now = func() time.Time { return now }
	output, err := newFileOutput(options, operations)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFileOutput(t, output)
	for i := 0; i < 3; i++ {
		if _, err := output.Write([]byte("{}\n")); err != nil {
			t.Fatal(err)
		}
	}
	files := managedFiles(t, options.Directory)
	if len(files) != 3 { // previous session + one backup + active
		t.Fatalf("backup count = %v", files)
	}
	now = now.Add(time.Hour)
	if err := output.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(managedFiles(t, options.Directory)) != 2 || string(readFile(t, oldFile)) != "{}\n" || string(readFile(t, foreign)) != "synthetic-existing-data" {
		t.Fatal("age cleanup affected active or pre-existing files")
	}
}

func TestFileOutputConcurrentWritesRemainCompleteAndBounded(t *testing.T) {
	options := fileOptions(t)
	options.MaxSizeBytes, options.MaxBackups = 60, 100
	output, err := NewFileOutput(options)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			for j := 0; j < 20; j++ {
				if _, err := output.Write([]byte("{}\n")); err != nil {
					t.Error(err)
				}
			}
		})
	}
	workers.Wait()
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	var events int
	for _, file := range managedFiles(t, options.Directory) {
		data := string(readFile(t, file))
		if len(data) > 60 || strings.ReplaceAll(data, "{}\n", "") != "" {
			t.Fatalf("incomplete or oversized segment: %q", data)
		}
		events += strings.Count(data, "{}\n")
	}
	if events != 160 {
		t.Fatalf("persisted events = %d", events)
	}
}

func TestFileOutputRejectsInvalidOptionsWithoutCreatingFiles(t *testing.T) {
	for _, change := range []func(*FileOptions){
		func(o *FileOptions) { o.Directory = "relative" },
		func(o *FileOptions) { o.MaxSizeBytes = 0 },
		func(o *FileOptions) { o.MaxSizeBytes = 1<<30 + 1 },
		func(o *FileOptions) { o.MaxBackups = -1 },
		func(o *FileOptions) { o.MaxBackups = 101 },
		func(o *FileOptions) { o.MaxAge = 0 },
		func(o *FileOptions) { o.MaxAge = 366 * 24 * time.Hour },
	} {
		options := fileOptions(t)
		change(&options)
		if _, err := NewFileOutput(options); err == nil {
			t.Fatalf("accepted invalid options: %+v", options)
		}
	}
}
