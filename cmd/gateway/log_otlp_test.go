package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/config"
	"github.com/artifact-gateway/artifact-gateway/internal/operationalog"
	collector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestRuntimeLogMirrorAttemptsAllDestinationsWhenAnyFails(t *testing.T) {
	for _, failed := range []string{"primary", "file", "otlp"} {
		t.Run(failed, func(t *testing.T) {
			var destinations = map[string]*bytes.Buffer{"primary": {}, "file": {}, "otlp": {}}
			failure := errors.New("synthetic-destination-error")
			writer := func(name string) io.Writer {
				return writerFunc(func(data []byte) (int, error) {
					_, _ = destinations[name].Write(data)
					if failed == name {
						return 1, failure
					}
					return len(data), nil
				})
			}
			var fallback bytes.Buffer
			mirror := &runtimeLogMirror{primary: writer("primary"), file: writer("file"), logs: writer("otlp"), report: operationalog.NewLogger(&fallback, "synthetic", "synthetic"), now: time.Now}
			n, err := mirror.Write([]byte("{}\n"))
			wantN := 3
			if failed == "primary" {
				wantN = 1
			}
			if n != wantN || !errors.Is(err, failure) {
				t.Fatalf("mirror result = %d, %v", n, err)
			}
			for name, buffer := range destinations {
				if buffer.String() != "{}\n" {
					t.Errorf("%s was skipped", name)
				}
			}
			if bytes.Contains(fallback.Bytes(), []byte("synthetic-destination-error")) {
				t.Error("fallback leaked the destination error")
			}
		})
	}
}

func TestRuntimeFileAndOTLPOutputIncludeResourceCleanupWithoutChangingExitCode(t *testing.T) {
	var received []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		var request collector.ExportLogsServiceRequest
		if err != nil || proto.Unmarshal(data, &request) != nil || bytes.Contains(data, []byte("synthetic-cleanup-secret")) {
			t.Error("invalid or unredacted wire event")
		}
		mu.Lock()
		defer mu.Unlock()
		for _, resource := range request.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, record := range scope.LogRecords {
					received = append(received, record.Body.GetStringValue())
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	var primary, fallback bytes.Buffer
	cfg := config.Config{InstanceID: "synthetic-instance", LogFileDirectory: t.TempDir(), LogFileMaxBytes: 4096, LogFileMaxBackups: 2, LogFileMaxAge: time.Hour, OTLPLogsEndpoint: server.URL, OTLPLogsQueueSize: 16, OTLPLogsTimeout: time.Second, OTLPLogsShutdownTimeout: time.Second}
	output, err := newRuntimeLogOutput(cfg, "synthetic-session", &primary, operationalog.NewLogger(&fallback, "synthetic", "synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	logger := operationalog.NewLogger(output, "synthetic-instance", "synthetic-session")
	code, err := runWithLogOutput(output, func() int {
		defer logger.Info("synthetic resource cleanup", "credentials", "synthetic-cleanup-secret")
		logger.Info("synthetic runtime stopped")
		return 7
	})
	if code != 7 || err != nil || fallback.Len() != 0 {
		t.Fatalf("runtime exit=%d cleanup=%v fallback=%q", code, err, fallback.String())
	}
	files, err := filepath.Glob(filepath.Join(cfg.LogFileDirectory, "gateway-*", "active.ndjson"))
	if err != nil || len(files) != 1 {
		t.Fatalf("runtime files=%v error=%v", files, err)
	}
	file, err := os.ReadFile(files[0])
	if err != nil || !bytes.Equal(file, primary.Bytes()) || bytes.Contains(file, []byte("synthetic-cleanup-secret")) {
		t.Error("file/stdout events or redaction differ")
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(received, ",") != "synthetic runtime stopped,synthetic resource cleanup" {
		t.Fatalf("OTLP cleanup order=%v", received)
	}
	if err := output.Close(); err != nil {
		t.Error(err)
	}
	if _, err := output.Write([]byte("{}\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Error("closed resource output accepted a record")
	}
}

func TestRuntimeOTLPFailureReporterIsConcurrentAndCountOnly(t *testing.T) {
	var fallback bytes.Buffer
	report := newOTLPReporter(operationalog.NewLogger(&fallback, "synthetic", "synthetic"))
	stats := operationalog.OTLPStats{ExportFailures: 3, UnconfirmedRecords: 4, RejectedEvents: 5, QueueRejectedEvents: 2, PendingRecords: 1, CleanupFailures: 1}
	var workers sync.WaitGroup
	for i := 0; i < 64; i++ {
		workers.Go(func() { report(stats) })
	}
	workers.Wait()
	text := fallback.String()
	if strings.Count(text, "runtime OTLP log output failed") != 1 || !strings.Contains(text, `"unconfirmed_records":4`) || !strings.Contains(text, `"queue_rejected_events":2`) || !strings.Contains(text, `"cleanup_failures":1`) {
		t.Fatalf("unsafe or unbounded diagnostic=%q", text)
	}
}
