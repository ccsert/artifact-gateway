package operationalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	collector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

func otlpTestOptions(endpoint string) OTLPOptions {
	return OTLPOptions{Endpoint: endpoint, InstanceID: "synthetic-instance", SessionID: "synthetic-session", QueueSize: 256, BatchSize: 64, ExportInterval: time.Hour, Timeout: time.Second}
}

func shutdownOTLP(t *testing.T, output *OTLPOutput) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := output.Shutdown(ctx); err != nil {
		t.Error(err)
	}
}

func flushOTLP(t *testing.T, output *OTLPOutput) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := output.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
}

func wireAttributes(values []*common.KeyValue) map[string]*common.AnyValue {
	result := make(map[string]*common.AnyValue)
	for _, value := range values {
		result[value.Key] = value.Value
	}
	return result
}

func TestOTLPOutputMapsSameRedactedEventToOfficialHTTPProtobuf(t *testing.T) {
	var request collector.ExportLogsServiceRequest
	var requestMu sync.Mutex
	var wireBody []byte
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/logs" || r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("unexpected protocol request: %s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		requestMu.Lock()
		defer requestMu.Unlock()
		wireBody = body
		authorization = r.Header.Get("Authorization")
		if err != nil || proto.Unmarshal(body, &request) != nil {
			t.Error("cannot decode protobuf request")
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	options := otlpTestOptions(server.URL)
	options.Headers = map[string]string{"Authorization": "Bearer synthetic-header-marker"}
	output, err := NewOTLPOutput(options)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownOTLP(t, output)
	logger := NewLogger(output, "synthetic-instance", "synthetic-session").With("component", "synthetic-bound", "requestId", "synthetic-request")
	record := slog.NewRecord(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), slog.LevelWarn, "synthetic event", 0)
	record.AddAttrs(slog.String("component", "synthetic-record"), slog.String("traceId", "0123456789abcdef0123456789abcdef"), slog.Group("AUTHORIZATION", slog.Group("nested", slog.String("safe_key", "synthetic-sensitive-marker"))), slog.Group("safe", slog.Int("count", 3), slog.Bool("ok", true)), slog.Uint64("large_integer", ^uint64(0)))
	if err := logger.Handler().Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	flushOTLP(t, output)
	requestMu.Lock()
	defer requestMu.Unlock()
	if authorization != "Bearer synthetic-header-marker" || bytes.Contains(wireBody, []byte("synthetic-sensitive-marker")) || bytes.Contains(wireBody, []byte("synthetic-header-marker")) {
		t.Fatal("credential/group redaction boundary did not hold")
	}
	if len(request.ResourceLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs[0].LogRecords) != 1 {
		t.Fatalf("unexpected resources: %v", &request)
	}
	resource := wireAttributes(request.ResourceLogs[0].Resource.Attributes)
	if resource["service.name"].GetStringValue() != "artifact-gateway" || resource["service.instance.id"].GetStringValue() != "synthetic-instance" {
		t.Fatalf("resource = %v", resource)
	}
	event := request.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
	attrs := wireAttributes(event.Attributes)
	if event.Body.GetStringValue() != "synthetic event" || event.SeverityNumber != logs.SeverityNumber_SEVERITY_NUMBER_WARN || event.SeverityText != "WARN" || event.TimeUnixNano != uint64(record.Time.UnixNano()) || event.ObservedTimeUnixNano == 0 || len(event.TraceId) != 16 || len(event.SpanId) != 0 {
		t.Fatalf("native event mapping = %v", event)
	}
	if attrs["component"].GetStringValue() != "synthetic-record" || attrs["requestId"].GetStringValue() != "synthetic-request" || attrs["instanceId"].GetStringValue() != "synthetic-instance" || attrs["sessionId"].GetStringValue() != "synthetic-session" || attrs["large_integer"].GetStringValue() != "18446744073709551615" {
		t.Fatalf("shared fields = %v", attrs)
	}
	nested := wireAttributes(wireAttributes(attrs["AUTHORIZATION"].GetKvlistValue().Values)["nested"].GetKvlistValue().Values)
	if nested["safe_key"].GetStringValue() != "[redacted]" || wireAttributes(attrs["safe"].GetKvlistValue().Values)["count"].GetIntValue() != 3 {
		t.Fatalf("group mapping = %v", attrs)
	}
}

func TestOTLPOutputConcurrentEmissionInvalidTraceAndShutdown(t *testing.T) {
	var count atomic.Int64
	var nativeTrace atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		var request collector.ExportLogsServiceRequest
		if err != nil || proto.Unmarshal(data, &request) != nil {
			t.Error("invalid protocol data")
		}
		for _, resource := range request.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, record := range scope.LogRecords {
					count.Add(1)
					if len(record.TraceId) != 0 || len(record.SpanId) != 0 {
						nativeTrace.Store(true)
					}
					if wireAttributes(record.Attributes)["traceId"].GetStringValue() != "synthetic-correlation-label" {
						t.Error("invalid trace label was lost")
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	output, err := NewOTLPOutput(otlpTestOptions(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			for j := 0; j < 10; j++ {
				NewLogger(output, "synthetic", "synthetic").Info("synthetic", "traceId", "synthetic-correlation-label")
			}
		})
	}
	workers.Wait()
	shutdownOTLP(t, output)
	shutdownOTLP(t, output)
	if count.Load() != 80 || nativeTrace.Load() {
		t.Fatalf("events = %d, fabricated native IDs = %v", count.Load(), nativeTrace.Load())
	}
	if _, err := output.Write([]byte("{}\n")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after shutdown = %v", err)
	}
}

func TestOTLPOutputRejectsBadInputsBeforeExport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid event was exported") }))
	defer server.Close()
	output, err := NewOTLPOutput(otlpTestOptions(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownOTLP(t, output)
	for _, data := range []string{"{}\n", "{}\n{}\n", "{\"time\":\"bad\",\"level\":\"INFO\",\"msg\":\"synthetic\"}\n", "{\"time\":\"2026-01-01T00:00:00Z\",\"level\":\"bad\",\"msg\":\"synthetic\"}\n", strings.Repeat("x", 65537), "{\"time\":\"2026-01-01T00:00:00Z\",\"level\":\"INFO\",\"msg\":\"synthetic\",\"deep\":" + strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40) + "}\n"} {
		if n, err := output.Write([]byte(data)); n != 0 || err == nil {
			t.Fatalf("accepted invalid event of %d bytes: %d, %v", len(data), n, err)
		}
	}
}

func TestOTLPOutputReportsPartialFailureWithoutRetryOrRawError(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		body, err := proto.Marshal(&collector.ExportLogsServiceResponse{PartialSuccess: &collector.ExportLogsPartialSuccess{RejectedLogRecords: 1, ErrorMessage: "synthetic-response-secret-marker"}})
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(body)
	}))
	defer server.Close()
	options := otlpTestOptions(server.URL)
	var reports atomic.Int64
	options.Report = func(stats OTLPStats) {
		if stats.ExportFailures == 0 || stats.UnconfirmedRecords == 0 {
			t.Error("missing failure counters")
		}
		reports.Add(1)
	}
	output, err := NewOTLPOutput(options)
	if err != nil {
		t.Fatal(err)
	}
	NewLogger(output, "synthetic", "synthetic").Info("synthetic")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := output.ForceFlush(ctx); err == nil {
		t.Fatal("partial success was invisible to flush")
	}
	if err := output.Shutdown(ctx); err == nil {
		t.Fatal("failure state was lost on shutdown")
	}
	if requests.Load() != 1 || reports.Load() != 1 || output.Stats().ExportFailures != 1 {
		t.Fatalf("partial success retry/counters = %d, %d, %+v", requests.Load(), reports.Load(), output.Stats())
	}
	// The diagnostics callback carries counts only, never response text or headers.
	statsJSON, err := json.Marshal(output.Stats())
	if err != nil || bytes.Contains(statsJSON, []byte("synthetic-response-secret-marker")) {
		t.Fatalf("unsafe diagnostic state = %s, %v", statsJSON, err)
	}
}
