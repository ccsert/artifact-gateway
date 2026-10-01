package operationalog

import (
	"bytes"
	"context"
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
	logs "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestOTLPOutputPreservesTypesAndOverridesExporterEnvironment(t *testing.T) {
	for _, name := range []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"} {
		t.Setenv(name, "https://synthetic-unrelated.invalid/wrong")
	}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_LOGS_HEADERS"} {
		t.Setenv(name, "Authorization=synthetic-unrelated-secret")
	}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_COMPRESSION", "OTEL_EXPORTER_OTLP_LOGS_COMPRESSION"} {
		t.Setenv(name, "gzip")
	}
	for _, name := range []string{"OTEL_EXPORTER_OTLP_TIMEOUT", "OTEL_EXPORTER_OTLP_LOGS_TIMEOUT"} {
		t.Setenv(name, "1")
	}
	var request collector.ExportLogsServiceRequest
	var mu sync.Mutex
	client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.String() != "https://synthetic-collector.invalid/custom/logs" || r.Header.Get("Content-Type") != "application/x-protobuf" || r.Header.Get("Content-Encoding") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("explicit Logs request inherited exporter configuration: %s %s %v", r.Method, r.URL, r.Header)
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) < 100*time.Millisecond {
			t.Error("explicit timeout did not override environment")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		err = proto.Unmarshal(data, &request)
		mu.Unlock()
		return otlpSyntheticResponse(r, http.StatusOK, nil), err
	})}
	output, err := newOTLPOutput(otlpTestOptions("https://synthetic-collector.invalid/custom/logs"), client)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownOTLP(t, output)
	logger := NewLogger(output, "synthetic-instance", "synthetic-session")
	timestamp := time.Date(2026, 10, 1, 2, 3, 4, 567890123, time.UTC)
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
		record := slog.NewRecord(timestamp, level, "synthetic typed event", 0)
		record.AddAttrs(slog.Any("values", []any{nil, true, int64(-9223372036854775808), int64(9223372036854775807), uint64(18446744073709551615), 1.25}), slog.Any("map", map[string]any{"number": int64(9007199254740993), "ok": false}))
		if err := logger.Handler().Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	flushOTLP(t, output)
	mu.Lock()
	defer mu.Unlock()
	if len(request.ResourceLogs) != 1 || len(request.ResourceLogs[0].ScopeLogs) != 1 {
		t.Fatalf("resources = %v", &request)
	}
	events := request.ResourceLogs[0].ScopeLogs[0].LogRecords
	if len(events) != 4 {
		t.Fatalf("events = %d", len(events))
	}
	for i, event := range events {
		if event.TimeUnixNano != uint64(timestamp.UnixNano()) || event.SeverityNumber != logs.SeverityNumber(5+i*4) || event.Body.GetStringValue() != "synthetic typed event" {
			t.Errorf("native event[%d] = %v", i, event)
		}
		attrs := wireAttributes(event.Attributes)
		values := attrs["values"].GetArrayValue().GetValues()
		if len(values) != 6 || values[0].Value != nil || !values[1].GetBoolValue() || values[2].GetIntValue() != -9223372036854775808 || values[3].GetIntValue() != 9223372036854775807 || values[4].GetStringValue() != "18446744073709551615" || values[5].GetDoubleValue() != 1.25 {
			t.Errorf("typed attributes = %v", values)
		}
		if wireAttributes(attrs["map"].GetKvlistValue().GetValues())["number"].GetIntValue() != 9007199254740993 {
			t.Error("integer precision was lost")
		}
	}
}

func TestOTLPOutputRetriesTransientFailuresAndKeepsPermanentFailuresVisible(t *testing.T) {
	for _, mode := range []string{"recovery", "permanent", "malformed-response", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int64
			client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
				attempt := requests.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				switch mode {
				case "recovery":
					if attempt < 3 {
						return otlpSyntheticResponse(r, http.StatusServiceUnavailable, nil), nil
					}
				case "permanent":
					return otlpSyntheticResponse(r, http.StatusBadRequest, []byte("synthetic-response-secret")), nil
				case "malformed-response":
					return otlpSyntheticResponse(r, http.StatusOK, []byte{0xff}), nil
				case "timeout":
					<-r.Context().Done()
					return nil, r.Context().Err()
				}
				return otlpSyntheticResponse(r, http.StatusOK, nil), nil
			})}
			options := otlpTestOptions("https://synthetic-collector.invalid")
			options.Timeout = 2 * time.Second
			if mode == "timeout" {
				options.Timeout = 100 * time.Millisecond
			}
			var reports atomic.Int64
			options.Report = func(OTLPStats) { reports.Add(1) }
			output, err := newOTLPOutput(options, client)
			if err != nil {
				t.Fatal(err)
			}
			NewLogger(output, "synthetic", "synthetic").Info("synthetic retry event")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			begin := time.Now()
			flushErr := output.ForceFlush(ctx)
			closeErr := output.Shutdown(ctx)
			stats := output.Stats()
			if mode == "recovery" {
				if requests.Load() != 3 || flushErr != nil || closeErr != nil || stats.ExportedRecords != 1 || stats.ExportFailures != 0 || reports.Load() != 0 {
					t.Fatalf("retry recovery: requests=%d flush=%v close=%v stats=%+v", requests.Load(), flushErr, closeErr, stats)
				}
			} else if requests.Load() != 1 || flushErr == nil || closeErr == nil || stats.ExportFailures != 1 || stats.UnconfirmedRecords != 1 || stats.PendingRecords != 0 || reports.Load() != 1 {
				t.Fatalf("failure accounting: requests=%d flush=%v close=%v stats=%+v reports=%d", requests.Load(), flushErr, closeErr, stats, reports.Load())
			}
			if mode == "timeout" && time.Since(begin) > time.Second {
				t.Error("export ignored its timeout")
			}
		})
	}
}

func TestOTLPOutputDoesNotFollowRedirectsWithLogsOrCredentials(t *testing.T) {
	var redirected atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	defer other.Close()
	for _, status := range []int{http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer synthetic-redirect-credential" {
					t.Error("initial receiver did not receive its configured credential")
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Location", other.URL)
				w.WriteHeader(status)
			}))
			defer server.Close()
			options := otlpTestOptions(server.URL)
			options.Headers = map[string]string{"Authorization": "Bearer synthetic-redirect-credential"}
			output, err := NewOTLPOutput(options)
			if err != nil {
				t.Fatal(err)
			}
			NewLogger(output, "synthetic", "synthetic").Info("synthetic redirect event")
			if err := output.Shutdown(context.Background()); err == nil {
				t.Error("redirect was acknowledged as delivery")
			}
			if requests.Load() != 1 || redirected.Load() != 0 || output.Stats().UnconfirmedRecords != 1 {
				t.Fatalf("redirect handling: initial=%d redirected=%d stats=%+v", requests.Load(), redirected.Load(), output.Stats())
			}
		})
	}
}

func TestOTLPOutputUsesClientTLSTrustAndRejectsUnknownCA(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	server.StartTLS()
	defer server.Close()
	trusted, err := newOTLPOutput(otlpTestOptions(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	NewLogger(trusted, "synthetic", "synthetic").Info("synthetic trusted TLS event")
	shutdownOTLP(t, trusted)
	if requests.Load() != 1 {
		t.Fatal("trusted synthetic CA did not receive the event")
	}
	unknown, err := NewOTLPOutput(otlpTestOptions(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	NewLogger(unknown, "synthetic", "synthetic").Info("synthetic unknown TLS event")
	if err := unknown.Shutdown(context.Background()); err == nil || requests.Load() != 1 || unknown.Stats().UnconfirmedRecords != 1 {
		t.Fatalf("unknown CA accepted: requests=%d stats=%+v err=%v", requests.Load(), unknown.Stats(), err)
	}
}

func TestOTLPOutputInvalidOptionsDoNotEchoSyntheticCredentials(t *testing.T) {
	for _, headers := range []map[string]string{{"hOsT": "synthetic-secret"}, {"Authorization": "synthetic-secret\nunsafe"}, {"Authorization": strings.Repeat("synthetic-secret", 400)}} {
		options := otlpTestOptions("https://synthetic-collector.invalid")
		options.Headers = headers
		if output, err := NewOTLPOutput(options); output != nil || err == nil || bytes.Contains([]byte(err.Error()), []byte("synthetic-secret")) {
			t.Fatalf("unsafe invalid-options result: %v", err)
		}
	}
}

func TestOTLPOutputResourceEnvironmentUsesSameSensitiveNamePolicy(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "AUTHORIZATION=synthetic-resource-secret,db.password=synthetic-resource-secret,safe.resource=synthetic-safe-value,service.name=synthetic-other-service")
	var request collector.ExportLogsServiceRequest
	client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if bytes.Contains(body, []byte("synthetic-resource-secret")) {
			t.Error("environment resource bypassed shared redaction")
		}
		if err == nil {
			err = proto.Unmarshal(body, &request)
		}
		return otlpSyntheticResponse(r, http.StatusOK, nil), err
	})}
	output, err := newOTLPOutput(otlpTestOptions("https://synthetic-resource.invalid"), client)
	if err != nil {
		t.Fatal(err)
	}
	NewLogger(output, "synthetic", "synthetic").Info("synthetic resource event")
	shutdownOTLP(t, output)
	attrs := wireAttributes(request.ResourceLogs[0].Resource.Attributes)
	if attrs["AUTHORIZATION"].GetStringValue() != "[redacted]" || attrs["db.password"].GetStringValue() != "[redacted]" || attrs["safe.resource"].GetStringValue() != "synthetic-safe-value" || attrs["service.name"].GetStringValue() != "artifact-gateway" {
		t.Fatalf("resource policy or explicit identity changed: %v", attrs)
	}
}

func TestOTLPOutputRejectsMalformedResourceBeforeSDKDiagnostics(t *testing.T) {
	for _, raw := range []string{"synthetic-resource-secret", "field=synthetic-resource-secret%zz"} {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", raw)
		output, err := NewOTLPOutput(otlpTestOptions("https://synthetic-resource.invalid"))
		if output != nil {
			shutdownOTLP(t, output)
		}
		if output != nil || err == nil || strings.Contains(err.Error(), "synthetic-resource-secret") {
			t.Errorf("malformed resource did not fail safely: %v", err)
		}
	}
}

func TestOTLPOutputHonorsRetryAfterWithinExportBudget(t *testing.T) {
	var calls atomic.Int64
	var elapsed time.Duration
	begin := time.Now()
	client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
		response := otlpSyntheticResponse(r, http.StatusOK, nil)
		if calls.Add(1) == 1 {
			begin = time.Now()
			response.StatusCode = http.StatusTooManyRequests
			response.Header.Set("Retry-After", "1")
		} else {
			elapsed = time.Since(begin)
		}
		return response, nil
	})}
	options := otlpTestOptions("https://synthetic-throttled.invalid")
	options.Timeout = 2 * time.Second
	output, err := newOTLPOutput(options, client)
	if err != nil {
		t.Fatal(err)
	}
	NewLogger(output, "synthetic", "synthetic").Info("synthetic throttled event")
	shutdownOTLP(t, output)
	if calls.Load() != 2 || elapsed < 900*time.Millisecond {
		t.Fatalf("receiver throttle ignored: requests=%d elapsed=%s", calls.Load(), elapsed)
	}
}

func TestOTLPOutputDoesNotRetryBeforeLongOrDateRetryAfter(t *testing.T) {
	for _, header := range []string{"10", "9223372036854775808", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)} {
		t.Run(header, func(t *testing.T) {
			var requests atomic.Int64
			client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
				requests.Add(1)
				response := otlpSyntheticResponse(r, http.StatusServiceUnavailable, nil)
				response.Header.Set("Retry-After", header)
				return response, nil
			})}
			options := otlpTestOptions("https://synthetic-throttled.invalid")
			options.Timeout = 100 * time.Millisecond
			output, err := newOTLPOutput(options, client)
			if err != nil {
				t.Fatal(err)
			}
			NewLogger(output, "synthetic", "synthetic").Info("synthetic throttled event")
			begin := time.Now()
			if err := output.Shutdown(context.Background()); err == nil || requests.Load() != 1 || time.Since(begin) > time.Second || output.Stats().UnconfirmedRecords != 1 {
				t.Fatalf("throttle exceeded budget or retried early: requests=%d stats=%+v error=%v", requests.Load(), output.Stats(), err)
			}
		})
	}
}

func TestOTLPOutputCompleteEventAndTimestampWireBounds(t *testing.T) {
	var requests atomic.Int64
	client := &http.Client{Transport: otlpSyntheticTransport(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return otlpSyntheticResponse(r, http.StatusOK, nil), nil
	})}
	output, err := newOTLPOutput(otlpTestOptions("https://synthetic-bounds.invalid"), client)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownOTLP(t, output)
	prefix := `{"time":"2026-01-01T00:00:00Z","level":"INFO","msg":"`
	suffix := "\"}\n"
	for _, size := range []int{65536, 65537} {
		data := []byte(prefix + strings.Repeat("m", size-len(prefix)-len(suffix)) + suffix)
		n, err := output.Write(data)
		if size == 65536 && (n != 65536 || err != nil) {
			t.Fatalf("complete event at limit was rejected: n=%d err=%v", n, err)
		}
		if size == 65537 && (n != 0 || err == nil) {
			t.Fatalf("valid oversized event was admitted: n=%d err=%v", n, err)
		}
	}
	for _, timestamp := range []string{"1969-12-31T23:59:59.999999999Z", "2554-07-21T23:34:33.709551616Z", "9999-12-31T00:00:00Z"} {
		data := []byte(`{"time":"` + timestamp + `","level":"INFO","msg":"synthetic out-of-range time"}` + "\n")
		if n, err := output.Write(data); n != 0 || err == nil {
			t.Fatalf("out-of-range timestamp admitted: %s n=%d err=%v", timestamp, n, err)
		}
	}
	flushOTLP(t, output)
	if requests.Load() != 1 || output.Stats().AcceptedRecords != 1 || output.Stats().RejectedEvents != 4 {
		t.Fatalf("wire bounds accounting: requests=%d stats=%+v", requests.Load(), output.Stats())
	}
}
