package operationalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

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
