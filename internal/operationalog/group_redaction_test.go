package operationalog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestNewLoggerRedactsSensitiveGroupContents(t *testing.T) {
	const secret = "private-group-value"
	for _, test := range []struct {
		name   string
		emit   func(*slog.Logger)
		groups []string
	}{
		{"record group", func(l *slog.Logger) {
			l.Info("event", slog.Group("Credentials", "value", secret), slog.Group("details", "value", "public-value"))
		}, []string{"Credentials"}},
		{"nested sensitive ancestor", func(l *slog.Logger) {
			l.Info("event", slog.Group("credentials", slog.Group("details", "value", secret)))
		}, []string{"credentials", "details"}},
		{"bound group", func(l *slog.Logger) {
			l.With(slog.Group("upstreamToken", "value", secret)).Info("event")
		}, []string{"upstreamToken"}},
		{"handler group", func(l *slog.Logger) {
			l.WithGroup("Authorization").Info("event", "value", secret)
		}, []string{"Authorization"}},
		{"bound handler group", func(l *slog.Logger) {
			l.WithGroup("authorization").With("value", secret).Info("event")
		}, []string{"authorization"}},
		{"nested handler group", func(l *slog.Logger) {
			l.WithGroup("credentials").WithGroup("details").Info("event", "value", secret)
		}, []string{"credentials", "details"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			test.emit(NewLogger(&output, "node-1", "session-1"))
			if bytes.Contains(output.Bytes(), []byte(secret)) {
				t.Errorf("sensitive group value reached output: %s", output.String())
			}
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["instanceId"] != "node-1" || record["msg"] != "event" {
				t.Fatalf("group redaction affected outer fields: %#v", record)
			}
			if details, ok := record["details"].(map[string]any); ok && details["value"] != "public-value" {
				t.Fatalf("safe sibling was redacted: %#v", details)
			}
			for _, name := range test.groups {
				group, ok := record[name].(map[string]any)
				if !ok {
					t.Fatalf("missing group %q: %#v", name, record)
				}
				record = group
			}
			if record["value"] != "[redacted]" {
				t.Fatalf("sensitive group value=%#v, want [redacted]", record["value"])
			}
		})
	}
}

func TestNewLoggerSensitiveGroupsAreRedactedBeforeBufferSearch(t *testing.T) {
	const secret = "private-search-value"
	var output bytes.Buffer
	buffer := NewBuffer(2)
	NewLogger(io.MultiWriter(&output, buffer), "node-1", "session-1").Info("event", slog.Group("credentials", "value", secret), slog.Group("details", "value", "public-value"))
	filter := Filter{To: time.Now().Add(time.Minute), Limit: 2, Keyword: secret}
	if page := buffer.Query(context.Background(), filter); len(page.Items) != 0 {
		t.Fatalf("sensitive group value remained searchable: %#v", page)
	}
	filter.Keyword = "redacted"
	if page := buffer.Query(context.Background(), filter); len(page.Items) != 1 {
		t.Fatalf("redacted event was lost: %#v", page)
	}
	filter.Keyword = "public-value"
	if page := buffer.Query(context.Background(), filter); len(page.Items) != 1 {
		t.Fatalf("safe sibling was lost: %#v", page)
	}
}
