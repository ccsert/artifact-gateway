package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
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

func newRuntimeLogOutput(cfg config.Config, sessionID string, primary io.Writer, fallback *slog.Logger) (*operationalog.Output, error) {
	if cfg.LogFileDirectory == "" && cfg.OTLPLogsEndpoint == "" {
		return operationalog.NewOutput(primary, nil, nil), nil
	}
	var file *operationalog.Output
	var logs *operationalog.OTLPOutput
	var err error
	if cfg.LogFileDirectory != "" {
		file, err = operationalog.NewFileOutput(operationalog.FileOptions{
			Directory: cfg.LogFileDirectory, MaxSizeBytes: cfg.LogFileMaxBytes,
			MaxBackups: cfg.LogFileMaxBackups, MaxAge: cfg.LogFileMaxAge,
		})
		if err != nil {
			return nil, err
		}
	}
	if cfg.OTLPLogsEndpoint != "" {
		logs, err = operationalog.NewOTLPOutput(operationalog.OTLPOptions{
			Endpoint: cfg.OTLPLogsEndpoint, Headers: cfg.OTLPLogsHeaders,
			InstanceID: cfg.InstanceID, SessionID: sessionID,
			QueueSize: cfg.OTLPLogsQueueSize, BatchSize: min(64, cfg.OTLPLogsQueueSize),
			ExportInterval: time.Second, Timeout: cfg.OTLPLogsTimeout, Report: newOTLPReporter(fallback),
		})
		if err != nil {
			if file != nil {
				err = errors.Join(err, file.Close())
			}
			return nil, err
		}
	}
	mirror := &runtimeLogMirror{primary: primary, report: fallback, now: time.Now}
	if file != nil {
		mirror.file = file
	}
	if logs != nil {
		mirror.logs = logs
	}
	flush := func() error {
		var fileErr, logsErr error
		if file != nil {
			fileErr = file.Flush()
		}
		if logs != nil {
			ctx, cancel := context.WithTimeout(context.Background(), cfg.OTLPLogsShutdownTimeout)
			logsErr = logs.ForceFlush(ctx)
			cancel()
		}
		return errors.Join(fileErr, logsErr)
	}
	closeOutput := func() error {
		var fileErr, logsErr error
		if file != nil {
			fileErr = file.Close()
		}
		if logs != nil {
			ctx, cancel := context.WithTimeout(context.Background(), cfg.OTLPLogsShutdownTimeout)
			logsErr = logs.Shutdown(ctx)
			cancel()
		}
		return errors.Join(fileErr, logsErr)
	}
	return operationalog.NewOutput(mirror, flush, closeOutput), nil
}

// The outer Output serializes this mirror and its owned file lifecycle. Both
// destinations receive the same redacted JSON even if either write fails.
// Writes remain synchronous: a slow filesystem can delay a logging caller.
type runtimeLogMirror struct {
	primary      io.Writer
	file         io.Writer
	logs         io.Writer
	report       *slog.Logger
	now          func() time.Time
	lastReport   time.Time
	failureCount uint64
}

func (m *runtimeLogMirror) Write(data []byte) (int, error) {
	n, primaryErr := m.primary.Write(data)
	if primaryErr == nil && n != len(data) {
		primaryErr = io.ErrShortWrite
	}
	var fileErr error
	if m.file != nil {
		fileN, err := m.file.Write(data)
		fileErr = err
		if fileErr == nil && fileN != len(data) {
			fileErr = io.ErrShortWrite
		}
	}
	if fileErr != nil {
		m.failureCount++
		now := m.now()
		if m.lastReport.IsZero() || now.Sub(m.lastReport) >= time.Minute {
			m.lastReport = now
			m.report.Error("runtime file log output failed", "error", fileErr, "failure_count", m.failureCount)
		}
	}
	var logsErr error
	if m.logs != nil {
		logsN, err := m.logs.Write(data)
		logsErr = err
		if logsErr == nil && logsN != len(data) {
			logsErr = io.ErrShortWrite
		}
	}
	return n, errors.Join(primaryErr, fileErr, logsErr)
}

func newOTLPReporter(fallback *slog.Logger) func(operationalog.OTLPStats) {
	var mu sync.Mutex
	var last time.Time
	return func(stats operationalog.OTLPStats) {
		mu.Lock()
		defer mu.Unlock()
		if !last.IsZero() && time.Since(last) < time.Minute {
			return
		}
		last = time.Now()
		fallback.Error("runtime OTLP log output failed", "export_failures", stats.ExportFailures,
			"unconfirmed_records", stats.UnconfirmedRecords, "rejected_events", stats.RejectedEvents)
	}
}
