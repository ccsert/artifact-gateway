package operationalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/artifact-gateway/artifact-gateway/internal/requestcontext"
)

func TestNewLoggerEmitsIdentityAndRedactsSensitiveAttributes(t *testing.T) {
	var output bytes.Buffer
	logger := NewLogger(&output, "node-1", "session-1")
	ctx, ids := requestcontext.WithRequest(context.Background(), "request-42")
	logger.ErrorContext(ctx, "object store failed", "error", errors.New("secret=password"), "upstreamToken", "token-value", "component", "object-store", "operation", "connect", "host", "storage.example")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]any{"level": "ERROR", "instanceId": "node-1", "sessionId": "session-1", "component": "object-store", "operation": "connect", "requestId": ids.RequestID, "traceId": ids.TraceID, "error": "[redacted]", "upstreamToken": "[redacted]", "host": "storage.example"} {
		if got := record[key]; got != expected {
			t.Fatalf("%s=%#v want %#v", key, got, expected)
		}
	}
	if _, ok := record["time"]; !ok {
		t.Fatalf("missing timestamp: %#v", record)
	}
	if bytes.Contains(output.Bytes(), []byte("password")) || bytes.Contains(output.Bytes(), []byte("token-value")) {
		t.Fatalf("sensitive values in output: %s", output.String())
	}
}

func TestNewLoggerKeepsSchemaForProcessEvents(t *testing.T) {
	var output bytes.Buffer
	NewLogger(&output, "node-2", "session-2").Info("gateway ready")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["component"] != "gateway" || record["operation"] != "event" || record["requestId"] != "" || record["traceId"] != "" {
		t.Fatalf("process event schema=%#v", record)
	}
}

func TestNewLoggerPreservesBoundFieldsAndRecordOverrides(t *testing.T) {
	ctx, ids := requestcontext.WithRequest(context.Background(), "context-request")
	for _, test := range []struct {
		name     string
		record   []any
		expected map[string]any
	}{
		{"bound fields", nil, map[string]any{"component": "worker", "operation": "job.run", "requestId": "bound-request", "traceId": "bound-trace"}},
		{"record overrides", []any{"component", "scheduler", "operation", "job.retry", "requestId", "record-request", "traceId", "record-trace"}, map[string]any{"component": "scheduler", "operation": "job.retry", "requestId": "record-request", "traceId": "record-trace"}},
		{"partial record override", []any{"operation", "job.retry"}, map[string]any{"component": "worker", "operation": "job.retry", "requestId": "bound-request", "traceId": "bound-trace"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := NewLogger(&output, "node-1", "session-1").With("component", "worker", "operation", "job.run").With("requestId", "bound-request", "traceId", "bound-trace")
			logger.InfoContext(ctx, "job event", test.record...)
			assertLogFields(t, output.Bytes(), test.expected)
			if bytes.Contains(output.Bytes(), []byte(ids.RequestID)) || bytes.Contains(output.Bytes(), []byte(ids.TraceID)) {
				t.Fatalf("context IDs overrode explicit fields: %s", output.String())
			}
		})
	}
}

func TestNewLoggerDerivedLoggersAreIsolated(t *testing.T) {
	var output bytes.Buffer
	base := NewLogger(&output, "node-1", "session-1")
	worker := base.With("component", "worker")
	job := worker.With("operation", "job.run", "requestId", "job-request")
	scheduler := base.With("component", "scheduler", "traceId", "scheduler-trace")
	for _, test := range []struct {
		logger   *slog.Logger
		expected map[string]any
	}{
		{job, map[string]any{"component": "worker", "operation": "job.run", "requestId": "job-request", "traceId": ""}},
		{worker, map[string]any{"component": "worker", "operation": "event", "requestId": "", "traceId": ""}},
		{scheduler, map[string]any{"component": "scheduler", "operation": "event", "requestId": "", "traceId": "scheduler-trace"}},
		{base, map[string]any{"component": "gateway", "operation": "event", "requestId": "", "traceId": ""}},
	} {
		output.Reset()
		test.logger.Info("event")
		assertLogFields(t, output.Bytes(), test.expected)
	}
}

func TestNewLoggerFieldsRespectGroupScopes(t *testing.T) {
	for _, test := range []struct {
		name     string
		derive   func(*slog.Logger) *slog.Logger
		record   []any
		group    string
		expected map[string]any
	}{
		{"empty group retains bindings", func(l *slog.Logger) *slog.Logger { return l.With("component", "worker").WithGroup("") }, nil, "", map[string]any{"component": "worker", "operation": "event"}},
		{"named attribute group does not bind outer fields", func(l *slog.Logger) *slog.Logger { return l.With(slog.Group("job", "component", "worker")) }, []any{slog.Group("details", "operation", "job.run")}, "", map[string]any{"component": "gateway", "operation": "event"}},
		{"inline bound group binds fields", func(l *slog.Logger) *slog.Logger {
			return l.With(slog.Group("", "component", "worker", "operation", "job.run"))
		}, nil, "", map[string]any{"component": "worker", "operation": "job.run"}},
		{"inline record group overrides defaults", func(l *slog.Logger) *slog.Logger { return l }, []any{slog.Group("", "component", "worker", "operation", "job.run")}, "", map[string]any{"component": "worker", "operation": "job.run"}},
		{"group bindings preserved", func(l *slog.Logger) *slog.Logger {
			return l.WithGroup("job").With("component", "worker", "operation", "job.run")
		}, nil, "job", map[string]any{"component": "worker", "operation": "job.run"}},
		{"outer binding does not suppress group defaults", func(l *slog.Logger) *slog.Logger { return l.With("component", "worker").WithGroup("job") }, nil, "job", map[string]any{"component": "gateway", "operation": "event"}},
		{"nested group gets its own defaults", func(l *slog.Logger) *slog.Logger {
			return l.WithGroup("job").With("component", "worker").WithGroup("step")
		}, nil, "job.step", map[string]any{"component": "gateway", "operation": "event"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			test.derive(NewLogger(&output, "node-1", "session-1")).Info("event", test.record...)
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if test.group != "" {
				for _, name := range strings.Split(test.group, ".") {
					group, ok := record[name].(map[string]any)
					if !ok {
						t.Fatalf("missing group %q: %#v", name, record)
					}
					record = group
				}
			}
			for key, expected := range test.expected {
				if got := record[key]; got != expected {
					t.Errorf("%s=%#v want %#v; output: %s", key, got, expected, output.String())
				}
			}
		})
	}
}

type logValueFunc func() slog.Value

func (f logValueFunc) LogValue() slog.Value { return f() }

func TestNewLoggerResolvesInlineLogValuersOnce(t *testing.T) {
	for _, bound := range []bool{true, false} {
		t.Run(fmt.Sprintf("bound=%t", bound), func(t *testing.T) {
			calls := 0
			attr := slog.Any("", logValueFunc(func() slog.Value {
				calls++
				return slog.GroupValue(slog.String("component", "worker"), slog.String("operation", "job.run"), slog.String("secretToken", "private-value"))
			}))
			var output bytes.Buffer
			logger := NewLogger(&output, "node-1", "session-1")
			if bound {
				logger.With(attr).Info("event")
			} else {
				logger.Info("event", attr)
			}
			if calls != 1 {
				t.Fatalf("LogValue calls=%d, want 1", calls)
			}
			assertLogFields(t, output.Bytes(), map[string]any{"component": "worker", "operation": "job.run", "secretToken": "[redacted]"})
			if bytes.Contains(output.Bytes(), []byte("private-value")) {
				t.Fatalf("bound or record value bypassed redaction: %s", output.String())
			}
		})
	}
}

func TestNewLoggerBoundFieldsRemainQueryable(t *testing.T) {
	buffer := NewBuffer(2)
	ctx, _ := requestcontext.WithRequest(context.Background(), "context-request")
	logger := NewLogger(buffer, "node-1", "session-1").With("component", "worker", "operation", "job.run", "requestId", "job-request", "traceId", "job-trace")
	logger.InfoContext(ctx, "job event")
	page := buffer.Query(context.Background(), Filter{To: time.Now().Add(time.Minute), Component: "worker", RequestID: "job-request", TraceID: "job-trace", Limit: 2})
	if len(page.Items) != 1 || page.Items[0].Operation != "job.run" {
		t.Fatalf("bound fields lost in query result: %#v", page)
	}
}

func assertLogFields(t *testing.T, line []byte, expected map[string]any) {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatal(err)
	}
	for key, value := range expected {
		if got := record[key]; got != value {
			t.Errorf("%s=%#v want %#v; output: %s", key, got, value, line)
		}
	}
}
