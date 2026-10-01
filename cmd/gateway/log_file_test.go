package main

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
)

func TestRuntimeFileOutputDisabledKeepsBorrowedDefault(t *testing.T) {
	var primary, fallback bytes.Buffer
	output, err := newRuntimeLogOutput(config.Config{}, &primary, operationalog.NewLogger(&fallback, "synthetic", "synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := output.Write([]byte("{}\n")); err != nil || primary.String() != "{}\n" || fallback.Len() != 0 {
		t.Fatalf("borrowed default = %q, %q, %v", primary.String(), fallback.String(), err)
	}
}

func TestRuntimeFileMirrorKeepsPrimaryOnFileErrorsAndReportsWithoutRecursion(t *testing.T) {
	var primary, fallback bytes.Buffer
	failure := errors.New("synthetic-sensitive-error-marker")
	secondary := writerFunc(func([]byte) (int, error) { return 0, failure })
	logger := operationalog.NewLogger(&fallback, "synthetic", "synthetic")
	mirror := &runtimeFileMirror{primary: &primary, file: secondary, report: logger, now: time.Now}
	output := operationalog.NewOutput(mirror, nil, nil)
	for i := 0; i < 10; i++ {
		if n, err := output.Write([]byte("{}\n")); n != 3 || !errors.Is(err, failure) {
			t.Fatalf("mirror = %d, %v", n, err)
		}
	}
	if primary.Len() != 30 || bytes.Count(fallback.Bytes(), []byte("runtime file log output failed")) != 1 || bytes.Contains(fallback.Bytes(), []byte("synthetic-sensitive-error-marker")) {
		t.Fatalf("failure isolation/reporting = %q, %q", primary.String(), fallback.String())
	}
}

func TestRuntimeFileMirrorAttemptsFileEvenWhenPrimaryFails(t *testing.T) {
	failure := errors.New("synthetic-primary-failure")
	var file, fallback bytes.Buffer
	mirror := &runtimeFileMirror{primary: writerFunc(func([]byte) (int, error) { return 1, failure }), file: &file, report: operationalog.NewLogger(&fallback, "synthetic", "synthetic"), now: time.Now}
	if n, err := mirror.Write([]byte("{}\n")); n != 1 || !errors.Is(err, failure) || file.String() != "{}\n" {
		t.Fatalf("primary failure = %d, %v, %q", n, err, file.String())
	}
}

func TestRuntimeFileOutputClosesAfterConfiguredRuntimeCleanup(t *testing.T) {
	var primary, fallback bytes.Buffer
	cfg := config.Config{LogFileDirectory: t.TempDir(), LogFileMaxBytes: 1024, LogFileMaxBackups: 2, LogFileMaxAge: time.Hour}
	output, err := newRuntimeLogOutput(cfg, &primary, operationalog.NewLogger(&fallback, "synthetic", "synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	code, err := runWithLogOutput(output, func() int {
		defer func() {
			if _, err := output.Write([]byte("{\"event\":\"synthetic-cleanup\"}\n")); err != nil {
				t.Error(err)
			}
		}()
		return 7
	})
	if err != nil || code != 7 {
		t.Fatalf("exit = %d, %v", code, err)
	}
	files, err := filepath.Glob(filepath.Join(cfg.LogFileDirectory, "gateway-*", "active.ndjson"))
	if err != nil || len(files) != 1 {
		t.Fatalf("runtime file = %v, %v", files, err)
	}
	if _, err := output.Write([]byte("{}\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("managed runtime output still open: %v", err)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(data []byte) (int, error) { return f(data) }
