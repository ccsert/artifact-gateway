package main

import (
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
)

// The runtime callback returns only after its HTTP and resource cleanup defers
// have run. Log output therefore stays available throughout that cleanup.
func runWithLogOutput(output *operationalog.Output, run func() int) (code int, cleanupErr error) {
	defer func() { cleanupErr = output.Close() }()
	return run(), nil
}

func newRuntimeLogOutput(cfg config.Config, primary io.Writer, fallback *slog.Logger) (*operationalog.Output, error) {
	if cfg.LogFileDirectory == "" {
		return operationalog.NewOutput(primary, nil, nil), nil
	}
	file, err := operationalog.NewFileOutput(operationalog.FileOptions{
		Directory: cfg.LogFileDirectory, MaxSizeBytes: cfg.LogFileMaxBytes,
		MaxBackups: cfg.LogFileMaxBackups, MaxAge: cfg.LogFileMaxAge,
	})
	if err != nil {
		return nil, err
	}
	mirror := &runtimeFileMirror{primary: primary, file: file, report: fallback, now: time.Now}
	return operationalog.NewOutput(mirror, file.Flush, file.Close), nil
}

// The outer Output serializes this mirror and its owned file lifecycle. Both
// destinations receive the same redacted JSON even if either write fails.
// Writes remain synchronous: a slow filesystem can delay a logging caller.
type runtimeFileMirror struct {
	primary      io.Writer
	file         io.Writer
	report       *slog.Logger
	now          func() time.Time
	lastReport   time.Time
	failureCount uint64
}

func (m *runtimeFileMirror) Write(data []byte) (int, error) {
	n, primaryErr := m.primary.Write(data)
	if primaryErr == nil && n != len(data) {
		primaryErr = io.ErrShortWrite
	}
	fileN, fileErr := m.file.Write(data)
	if fileErr == nil && fileN != len(data) {
		fileErr = io.ErrShortWrite
	}
	if fileErr != nil {
		m.failureCount++
		now := m.now()
		if m.lastReport.IsZero() || now.Sub(m.lastReport) >= time.Minute {
			m.lastReport = now
			m.report.Error("runtime file log output failed", "error", fileErr, "failure_count", m.failureCount)
		}
	}
	return n, errors.Join(primaryErr, fileErr)
}
